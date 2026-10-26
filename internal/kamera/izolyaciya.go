package kamera

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Izolyaciya — что закрыто в камере. По умолчанию камера — одиночка:
// без сети, вся система только на чтение, писать можно в рабочий
// каталог, в свой /tmp и в пути из Zapis.
type Izolyaciya struct {
	// Off выключает изоляцию: процесс видит всё, что видит пользователь.
	Off bool `json:"off,omitempty"`
	// Set оставляет сеть.
	Set bool `json:"set,omitempty"`
	// Zapis — дополнительные каталоги, куда можно писать.
	Zapis []string `json:"zapis,omitempty"`
	// BezDoma прячет домашний каталог целиком, а не только секреты.
	BezDoma bool `json:"bez_doma,omitempty"`
}

// On — изоляция включена.
func (iz Izolyaciya) On() bool { return !iz.Off }

// String — изоляция по-русски для shmon.
func (iz Izolyaciya) String() string {
	if iz.Off {
		return "выключена"
	}
	parts := []string{"сеть закрыта"}
	if iz.Set {
		parts[0] = "сеть открыта"
	}
	parts = append(parts, "система только на чтение")
	if iz.BezDoma {
		parts = append(parts, "домашний каталог спрятан")
	} else {
		parts = append(parts, "секреты в домашнем каталоге спрятаны")
	}
	return strings.Join(parts, ", ")
}

// Normalize проверяет пути для записи и делает их абсолютными
// без символических ссылок.
func (iz *Izolyaciya) Normalize() error {
	for i, p := range iz.Zapis {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("--zapis %s: %w", p, err)
		}
		fi, err := os.Stat(real)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return fmt.Errorf("--zapis %s: это не каталог", p)
		}
		iz.Zapis[i] = real
	}
	return nil
}

// Writable — каталоги, куда в камере можно писать: рабочий и Zapis.
func (iz Izolyaciya) Writable(dir string) []string {
	out := []string{}
	if dir != "" {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		out = append(out, dir)
	}
	for _, p := range iz.Zapis {
		if p != dir {
			out = append(out, p)
		}
	}
	return out
}

// RiskyDir сообщает, что рабочий каталог открывает на запись слишком
// много: корень или домашний каталог целиком.
func RiskyDir(dir string) bool {
	clean := filepath.Clean(dir)
	if clean == string(filepath.Separator) || filepath.Dir(clean) == clean {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == clean {
		return true
	}
	return false
}

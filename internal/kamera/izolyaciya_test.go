package kamera

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// needIzolyaciya пропускает тест, если стены на этой машине не строятся.
// В CI BALANDA_TEST_IZOLYACIYA=1, и тогда это ошибка, а не пропуск.
func needIzolyaciya(t *testing.T) {
	t.Helper()
	if err := ProbeIzolyaciya(); err != nil {
		if os.Getenv("BALANDA_TEST_IZOLYACIYA") == "1" {
			t.Fatalf("изоляция недоступна: %v", err)
		}
		t.Skipf("изоляция недоступна: %v", err)
	}
}

// runIz запускает помощника в камере. Его stderr попадает в лог теста,
// чтобы по упавшему CI было видно, что ответила система. Вывод идёт
// в файл: в AppContainer на Windows поток должен быть файлом.
func runIz(t *testing.T, s *Store, mode, arg string, iz Izolyaciya) int {
	t.Helper()
	t.Setenv("BALANDA_TEST_ARG", arg)
	errf, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer errf.Close()
	_, code, err := Sadit(s, RunOptions{
		Cmd:        helperCmd(t, mode),
		Izolyaciya: iz,
		Stdout:     io.Discard,
		Stderr:     errf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(errf.Name()); len(bytes.TrimSpace(out)) > 0 {
		t.Logf("%s %s: код %d: %s", mode, arg, code, bytes.TrimSpace(out))
	}
	return code
}

func TestIzolyaciyaSteny(t *testing.T) {
	needIzolyaciya(t)
	s := testStore(t)
	work := t.TempDir()
	outside := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(home, ".ssh", "id_test")
	if err := os.WriteFile(key, []byte("секрет"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(work)

	if code := runIz(t, s, "write", filepath.Join(work, "ok.txt"), Izolyaciya{}); code != 0 {
		t.Errorf("запись в рабочий каталог: код %d", code)
	}
	if code := runIz(t, s, "write", filepath.Join(outside, "nelzya.txt"), Izolyaciya{}); code == 0 {
		t.Error("камера записала файл за пределами рабочего каталога")
	}
	if _, err := os.Stat(filepath.Join(outside, "nelzya.txt")); err == nil {
		t.Error("файл за пределами рабочего каталога появился")
	}
	if code := runIz(t, s, "read", key, Izolyaciya{}); code == 0 {
		t.Error("камера прочитала ключ из ~/.ssh")
	}
	if code := runIz(t, s, "dial", "", Izolyaciya{}); code == 0 {
		t.Error("камера вышла в сеть")
	}
	if code := runIz(t, s, "write", filepath.Join(outside, "mozhno.txt"), Izolyaciya{Zapis: []string{outside}}); code != 0 {
		t.Errorf("запись в каталог из --zapis: код %d", code)
	}
	if code := runIz(t, s, "write", filepath.Join(outside, "bez.txt"), Izolyaciya{Off: true}); code != 0 {
		t.Errorf("без изоляции запись не прошла: код %d", code)
	}
	// На Linux и macOS секреты закрыты, даже если рабочий каталог — весь
	// дом. На Windows рабочий каталог открыт AppContainer целиком.
	if runtime.GOOS != "windows" {
		t.Chdir(home)
		if code := runIz(t, s, "read", key, Izolyaciya{}); code == 0 {
			t.Error("из домашнего каталога камера прочитала ключ из ~/.ssh")
		}
		if code := runIz(t, s, "write", filepath.Join(home, "ok.txt"), Izolyaciya{}); code != 0 {
			t.Errorf("запись в домашний каталог, когда он рабочий: код %d", code)
		}
	}
}

func TestIzolyaciyaNormalize(t *testing.T) {
	dir := t.TempDir()
	iz := Izolyaciya{Zapis: []string{dir}}
	if err := iz.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(iz.Zapis[0]) {
		t.Errorf("путь не абсолютный: %s", iz.Zapis[0])
	}
	bad := Izolyaciya{Zapis: []string{filepath.Join(dir, "net-takogo")}}
	if err := bad.Normalize(); err == nil {
		t.Error("несуществующий каталог: ждали ошибку")
	}
	if !RiskyDir(string(filepath.Separator)) && runtime.GOOS != "windows" {
		t.Error("корень должен считаться опасным рабочим каталогом")
	}
	if home, err := os.UserHomeDir(); err == nil && !RiskyDir(home) {
		t.Error("домашний каталог должен считаться опасным рабочим каталогом")
	}
	if RiskyDir(dir) {
		t.Error("обычный каталог посчитан опасным")
	}
}

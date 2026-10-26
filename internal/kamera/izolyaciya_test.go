package kamera

import (
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

func runIz(t *testing.T, s *Store, mode, arg string, iz Izolyaciya) int {
	t.Helper()
	t.Setenv("BALANDA_TEST_ARG", arg)
	_, code, err := Sadit(s, RunOptions{
		Cmd:        helperCmd(t, mode),
		Izolyaciya: iz,
		Stdout:     io.Discard,
		Stderr:     io.Discard,
	})
	if err != nil {
		t.Fatal(err)
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

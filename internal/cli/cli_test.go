package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Ar4ikov/balanda/internal/voda"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

func run(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestHelpIsRussian(t *testing.T) {
	out, _, code := run(t, "", "--help")
	if code != 0 {
		t.Fatalf("код %d", code)
	}
	for _, want := range []string{"Использование:", "Камера (песочница):", "sadit", "travit", "Флаги:", "показать эту справку"} {
		if !strings.Contains(out, want) {
			t.Errorf("в справке нет %q", want)
		}
	}
	for _, bad := range []string{"Usage:", "Available Commands", "Flags:", "help for", "[flags]", "(default"} {
		if strings.Contains(out, bad) {
			t.Errorf("в справке осталось английское %q", bad)
		}
	}
	if strings.Index(out, "sadit") > strings.Index(out, "otboy") {
		t.Error("команды отсортированы по алфавиту, а не по смыслу")
	}
	sub, _, _ := run(t, "", "nalit", "--help")
	if !strings.Contains(sub, "(по умолчанию 3)") || strings.Contains(sub, "default") {
		t.Errorf("значения по умолчанию не по-русски:\n%s", sub)
	}
}

func TestErrorsAreRussian(t *testing.T) {
	_, errOut, code := run(t, "", "sadti")
	if code != 1 || !strings.Contains(errOut, "нет такой команды: sadti") || !strings.Contains(errOut, "sadit") {
		t.Errorf("неизвестная команда: код %d, %q", code, errOut)
	}
	_, errOut, code = run(t, "", "nalit", "--bred")
	if code != 1 || !strings.Contains(errOut, "нет такого флага: --bred") {
		t.Errorf("неизвестный флаг: код %d, %q", code, errOut)
	}
	_, errOut, code = run(t, "", "shmon")
	if code != 1 || !strings.Contains(errOut, "нужно одна камера") && !strings.Contains(errOut, "нужно") {
		t.Errorf("нет аргумента: код %d, %q", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	out, _, code := run(t, "", "version")
	if code != 0 || !strings.HasPrefix(out, "balanda ") || !strings.Contains(out, "коммит:") || !strings.Contains(out, "собрано:") {
		t.Errorf("version: код %d\n%s", code, out)
	}
	short, _, _ := run(t, "", "version", "--short")
	if strings.Contains(strings.TrimSpace(short), "\n") {
		t.Errorf("--short печатает больше одной строки: %q", short)
	}
}

func TestNalitProba(t *testing.T) {
	a, _, code := run(t, "", "nalit", "-a", "2", "-s", "korporat", "--zerno", "7")
	b, _, _ := run(t, "", "pour", "--abzacy", "2", "--stil", "korporat", "--zerno", "7")
	if code != 0 || a != b || strings.Count(strings.TrimSpace(a), "\n\n") != 1 {
		t.Fatalf("nalit: код %d, одинаково: %v\n%s", code, a == b, a)
	}
	out, _, code := run(t, a, "proba", "-")
	if code != 0 || !strings.Contains(out, "Водность:") || !strings.Contains(out, "строка ") {
		t.Fatalf("proba: код %d\n%s", code, out)
	}
	_, errOut, code := run(t, a, "proba", "--porog", "10", "-k", "-")
	if code != 1 || !strings.Contains(errOut, "воды больше 10") {
		t.Errorf("--porog: код %d, %q", code, errOut)
	}
	_, _, code = run(t, "", "nalit", "--stil", "pravda")
	if code != 1 {
		t.Error("неизвестный стиль: ждали код 1")
	}
}

func TestProbaFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(f, []byte("\uFEFFПросто текст про кота, который спал на клавиатуре."), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "", "check", "--kratko", f)
	if code != 0 || !strings.Contains(out, "сухо") {
		t.Errorf("код %d\n%s", code, out)
	}
	_, errOut, code := run(t, "", "proba", filepath.Join(t.TempDir(), "net.md"))
	if code != 1 || errOut == "" {
		t.Errorf("нет файла: код %d", code)
	}
}

func TestTravit(t *testing.T) {
	out, _, code := run(t, "", "travit", "--tema", "db", "--zerno", "1")
	if code != 0 || !strings.Contains(out, "тема: база") || !strings.Contains(out, "--tema db") {
		t.Errorf("travit: код %d\n%s", code, out)
	}
	out, _, _ = run(t, "", "tell", "--spisok")
	for _, tema := range []string{"deploy", "git", "prod"} {
		if !strings.Contains(out, tema) {
			t.Errorf("в списке тем нет %s", tema)
		}
	}
	_, _, code = run(t, "", "travit", "--tema", "net")
	if code != 1 {
		t.Error("неизвестная тема: ждали код 1")
	}
}

func TestScanCmd(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "", "scan", "--bez-git", root)
	if code != 0 || !strings.Contains(out, "node_modules") || !strings.Contains(out, "Итого") {
		t.Fatalf("scan: код %d\n%s", code, out)
	}
	_, errOut, code := run(t, "", "svarit", "--bez-git", root)
	if code != 1 || !strings.Contains(errOut, "подтверждение") {
		t.Fatalf("svarit без терминала и --yes: код %d, %q", code, errOut)
	}
	out, _, code = run(t, "", "clean", "--bez-git", "--yes", root)
	if code != 0 || !strings.Contains(out, "Сварено 1 из 1") {
		t.Fatalf("svarit --yes: код %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("node_modules не удалён")
	}
}

func TestKameraCmds(t *testing.T) {
	t.Setenv("BALANDA_STATE_DIR", t.TempDir())
	out, _, code := run(t, "", "pereklichka")
	if code != 0 || !strings.Contains(out, "Камер нет") {
		t.Fatalf("pereklichka: код %d\n%s", code, out)
	}
	out, _, code = run(t, "", "otboy")
	if code != 0 || !strings.Contains(out, "гасить некого") {
		t.Fatalf("otboy: код %d\n%s", code, out)
	}
	_, errOut, code := run(t, "", "sadit", "--paika", "disk=1g", "true")
	if code != 1 || !strings.Contains(errOut, "неизвестный ключ") {
		t.Fatalf("плохая пайка: код %d, %q", code, errOut)
	}
	_, errOut, code = run(t, "", "shmon", "5")
	if code != 1 || !strings.Contains(errOut, "камеры 5 нет") {
		t.Fatalf("shmon несуществующей: код %d, %q", code, errOut)
	}
}

func TestHighlightWrap(t *testing.T) {
	r := voda.Proba("Стоит отметить, что данный модуль работает. Второе предложение довольно длинное и обычное.\nНовая строка.")
	got := highlight(vyvod.Palette{}, r, 30)
	for _, line := range strings.Split(got, "\n") {
		if n := utf8.RuneCountInString(line); n > 30 {
			t.Errorf("строка длиннее 30: %q", line)
		}
	}
	if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(r.Text), " ") {
		t.Errorf("перенос изменил текст:\n%s", got)
	}
	colored := highlight(vyvod.Palette{Red: "<r>", Yellow: "<y>", Underline: "", Reset: "</>"}, r, 0)
	if !strings.Contains(colored, "<r>Стоит</> <r>отметить</>") || !strings.Contains(colored, "<y>данный</>") {
		t.Errorf("подсветка: %s", colored)
	}
}

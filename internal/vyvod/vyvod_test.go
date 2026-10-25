package vyvod

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSize(t *testing.T) {
	tests := map[int64]string{
		-1:                "—",
		0:                 "0 Б",
		512:               "512 Б",
		1024:              "1 КБ",
		1536:              "1,5 КБ",
		10 << 20:          "10 МБ",
		150 << 20:         "150 МБ",
		3 * (1 << 30) / 2: "1,5 ГБ",
	}
	for n, want := range tests {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, ждали %q", n, got, want)
		}
	}
}

func TestPlural(t *testing.T) {
	tests := map[int64]string{1: "файл", 2: "файла", 5: "файлов", 11: "файлов", 12: "файлов", 21: "файл", 22: "файла", 111: "файлов", 104: "файла"}
	for n, want := range tests {
		if got := Plural(n, "файл", "файла", "файлов"); got != want {
			t.Errorf("Plural(%d) = %q, ждали %q", n, got, want)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := map[time.Duration]string{
		850 * time.Millisecond:        "850 мс",
		1200 * time.Millisecond:       "1,2 с",
		3 * time.Second:               "3 с",
		42 * time.Second:              "42 с",
		3*time.Minute + 5*time.Second: "3 мин 5 с",
		2 * time.Minute:               "2 мин",
		90 * time.Minute:              "1 ч 30 мин",
		-time.Second:                  "0 мс",
	}
	for d, want := range tests {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%s) = %q, ждали %q", d, got, want)
		}
	}
}

func TestTable(t *testing.T) {
	var b bytes.Buffer
	Table{
		Header: []string{"А", "ЧИСЛО"},
		Rows:   [][]string{{"длинное", "1"}, {"к", "100"}},
		Right:  map[int]bool{1: true},
	}.Write(&b, Palette{})
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("строк %d", len(lines))
	}
	if !strings.HasSuffix(lines[1], "    1") || !strings.HasSuffix(lines[2], "  100") {
		t.Errorf("правое выравнивание:\n%s", b.String())
	}
}

func TestConfirm(t *testing.T) {
	for in, want := range map[string]bool{"д\n": true, "Да\n": true, "y\n": true, "l\n": true, "н\n": false, "\n": false, "": false} {
		var out bytes.Buffer
		if got := Confirm(strings.NewReader(in), &out, "?"); got != want {
			t.Errorf("Confirm(%q) = %v", in, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("абвгдеж", 4); got != "абв…" {
		t.Errorf("Truncate = %q", got)
	}
	if got := Truncate("абв", 4); got != "абв" {
		t.Errorf("Truncate = %q", got)
	}
}

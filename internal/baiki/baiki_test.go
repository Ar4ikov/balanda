package baiki

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Ar4ikov/balanda/internal/voda"
)

func TestKorpus(t *testing.T) {
	temy, err := Temy()
	if err != nil {
		t.Fatal(err)
	}
	if len(temy) < 8 {
		t.Fatalf("тем %d, ждали хотя бы 8", len(temy))
	}
	seen := map[string]bool{}
	total := 0
	for _, tm := range temy {
		if tm.Title == "" {
			t.Errorf("%s: нет названия", tm.ID)
		}
		if len(tm.Baiki) < 5 {
			t.Errorf("%s: байек %d, ждали хотя бы 5", tm.ID, len(tm.Baiki))
		}
		for _, b := range tm.Baiki {
			total++
			if seen[b.Text] {
				t.Errorf("повтор: %q", b.Text)
			}
			seen[b.Text] = true
			if last, _ := utf8.DecodeLastRuneInString(b.Text); !strings.ContainsRune(".!?»", last) {
				t.Errorf("%s: байка без точки в конце: %q", tm.ID, b.Text)
			}
			if strings.Contains(b.Text, "%") && !strings.Contains(b.Text, "90%") {
				t.Errorf("%s: в байку попал разделитель: %q", tm.ID, b.Text)
			}
		}
	}
	if total < 40 {
		t.Errorf("байек всего %d, ждали хотя бы 40", total)
	}
}

func TestRasskazat(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	b, err := Rasskazat("git", rnd)
	if err != nil {
		t.Fatal(err)
	}
	if b.Tema != "git" || b.Text == "" {
		t.Fatalf("байка %+v", b)
	}
	if _, err := Rasskazat("net-takoy", rnd); err == nil {
		t.Error("несуществующая тема: ждали ошибку")
	}
	got := map[string]bool{}
	for i := 0; i < 200; i++ {
		b, err := Rasskazat("", rnd)
		if err != nil {
			t.Fatal(err)
		}
		got[b.Tema] = true
	}
	if len(got) < 5 {
		t.Errorf("за 200 попыток выпало тем: %d", len(got))
	}
}

// Байки — живой текст, proba не должна находить в них воду.
func TestBaikiAreDry(t *testing.T) {
	temy, err := Temy()
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range temy {
		for _, b := range tm.Baiki {
			if r := voda.Proba(b.Text); r.Score >= 40 {
				t.Errorf("%s: водность %d у байки %q", tm.ID, r.Score, b.Text)
			}
		}
	}
}

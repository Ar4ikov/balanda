// Package voda генерирует канцелярскую «воду» и оценивает водность текста.
package voda

import (
	"bufio"
	"embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed stili/*.txt
var stiliFS embed.FS

// Stil — набор шаблонов одного стиля.
type Stil struct {
	ID       string
	Title    string
	sections map[string][]string
}

var slotRe = regexp.MustCompile(`\{([^{}]+)\}`)

// Stili возвращает все встроенные стили по алфавиту.
func Stili() ([]*Stil, error) {
	entries, err := stiliFS.ReadDir("stili")
	if err != nil {
		return nil, err
	}
	var out []*Stil
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".txt")
		data, err := stiliFS.ReadFile("stili/" + e.Name())
		if err != nil {
			return nil, err
		}
		s, err := parseStil(id, string(data))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// StilIDs — имена стилей для справки и проверки флага.
func StilIDs() []string {
	all, err := Stili()
	if err != nil {
		return nil
	}
	ids := make([]string, len(all))
	for i, s := range all {
		ids[i] = s.ID
	}
	return ids
}

// GetStil ищет стиль по имени.
func GetStil(id string) (*Stil, error) {
	all, err := Stili()
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("нет стиля %q, есть: %s", id, strings.Join(StilIDs(), ", "))
}

func parseStil(id, data string) (*Stil, error) {
	s := &Stil{ID: id, sections: map[string][]string{}}
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "@"):
			cur = strings.TrimPrefix(trimmed, "@")
			if _, dup := s.sections[cur]; dup {
				return nil, fmt.Errorf("стиль %s, строка %d: раздел @%s повторяется", id, n, cur)
			}
			s.sections[cur] = nil
		case cur == "":
			return nil, fmt.Errorf("стиль %s, строка %d: текст до первого раздела", id, n)
		default:
			// Ведущий пробел значим: им «хвосты» приклеиваются к фразе.
			s.sections[cur] = append(s.sections[cur], line)
		}
	}
	if t := s.sections["название"]; len(t) > 0 {
		s.Title = strings.TrimSpace(t[0])
	}
	return s, s.validate()
}

// validate проверяет, что у шаблонов есть все разделы, а у разделов
// хватает вариантов.
func (s *Stil) validate() error {
	tpls := s.sections["шаблоны"]
	if len(tpls) == 0 {
		return fmt.Errorf("стиль %s: нет шаблонов", s.ID)
	}
	for _, t := range tpls {
		for _, m := range slotRe.FindAllStringSubmatch(t, -1) {
			if len(s.sections[m[1]]) == 0 {
				return fmt.Errorf("стиль %s: в шаблоне %q раздел {%s} пуст или не найден", s.ID, t, m[1])
			}
		}
	}
	return nil
}

// Templates — шаблоны предложений (для тестов).
func (s *Stil) Templates() []string { return s.sections["шаблоны"] }

// Generator льёт воду в одном стиле.
type Generator struct {
	stil *Stil
	rnd  *rand.Rand
}

// NewGenerator создаёт генератор. Одинаковое зерно даёт одинаковый текст.
func NewGenerator(s *Stil, seed uint64) *Generator {
	return &Generator{stil: s, rnd: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

// pick берёт вариант, по возможности ещё не использованный в абзаце.
func (g *Generator) pick(list []string, used map[string]bool) string {
	for try := 0; try < 8; try++ {
		v := list[g.rnd.IntN(len(list))]
		if !used[v] {
			used[v] = true
			return v
		}
	}
	return list[g.rnd.IntN(len(list))]
}

// Sentence собирает одно предложение.
func (g *Generator) Sentence(used map[string]bool) string {
	tpl := g.pick(g.stil.sections["шаблоны"], used)
	out := slotRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := m[1 : len(m)-1]
		return g.pick(g.stil.sections[name], used)
	})
	out = strings.Join(strings.Fields(out), " ")
	out = strings.ReplaceAll(out, " ,", ",")
	out = strings.ReplaceAll(out, ",,", ",")
	out = strings.ReplaceAll(out, ",.", ".")
	return capitalize(out)
}

// Paragraph — абзац из 3–5 предложений.
func (g *Generator) Paragraph() string {
	n := 3 + g.rnd.IntN(3)
	used := map[string]bool{}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = g.Sentence(used)
	}
	return strings.Join(parts, " ")
}

// Paragraphs — n абзацев.
func (g *Generator) Paragraphs(n int) ([]string, error) {
	if n < 1 || n > 1000 {
		return nil, errors.New("абзацев должно быть от 1 до 1000")
	}
	out := make([]string, n)
	for i := range out {
		out[i] = g.Paragraph()
	}
	return out, nil
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// Wrap переносит абзац по ширине width рун.
func Wrap(s string, width int) string {
	if width < 20 {
		return s
	}
	var b strings.Builder
	line := 0
	for i, w := range strings.Fields(s) {
		l := utf8.RuneCountInString(w)
		if i > 0 {
			if line+1+l > width {
				b.WriteByte('\n')
				line = 0
			} else {
				b.WriteByte(' ')
				line++
			}
		}
		b.WriteString(w)
		line += l
	}
	return b.String()
}

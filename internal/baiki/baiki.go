// Package baiki хранит корпус айтишных баек и выдаёт их по одной.
package baiki

import (
	"embed"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

//go:embed korpus/*.txt
var korpusFS embed.FS

// Baika — одна байка.
type Baika struct {
	Tema  string
	Title string
	Text  string
}

// Tema — тема с байками.
type Tema struct {
	ID    string
	Title string
	Baiki []Baika
}

// Temy читает корпус: файл на тему, байки разделены строкой «%»,
// первая строка «# название» — название темы.
func Temy() ([]Tema, error) {
	entries, err := korpusFS.ReadDir("korpus")
	if err != nil {
		return nil, err
	}
	var out []Tema
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".txt")
		data, err := korpusFS.ReadFile("korpus/" + e.Name())
		if err != nil {
			return nil, err
		}
		t, err := parseTema(id, strings.ReplaceAll(string(data), "\r\n", "\n"))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func parseTema(id, data string) (Tema, error) {
	t := Tema{ID: id, Title: id}
	if first, rest, ok := strings.Cut(data, "\n"); ok && strings.HasPrefix(first, "# ") {
		t.Title = strings.TrimSpace(strings.TrimPrefix(first, "# "))
		data = rest
	}
	for _, chunk := range strings.Split(data, "\n%\n") {
		text := strings.Join(strings.Fields(chunk), " ")
		if text == "" {
			continue
		}
		t.Baiki = append(t.Baiki, Baika{Tema: id, Title: t.Title, Text: text})
	}
	if len(t.Baiki) == 0 {
		return t, fmt.Errorf("тема %s пустая", id)
	}
	return t, nil
}

// Rasskazat выбирает случайную байку. Пустая тема — из всех.
func Rasskazat(tema string, rnd *rand.Rand) (Baika, error) {
	temy, err := Temy()
	if err != nil {
		return Baika{}, err
	}
	var pool []Baika
	var ids []string
	for _, t := range temy {
		ids = append(ids, t.ID)
		if tema == "" || t.ID == tema {
			pool = append(pool, t.Baiki...)
		}
	}
	if len(pool) == 0 {
		return Baika{}, fmt.Errorf("нет темы %q, есть: %s", tema, strings.Join(ids, ", "))
	}
	return pool[rnd.IntN(len(pool))], nil
}

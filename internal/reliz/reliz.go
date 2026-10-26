// Package reliz считает следующую версию по коммитам Conventional Commits
// и собирает заметки к релизу. Им пользуется workflow Release через
// tools/reliz.
package reliz

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Commit — коммит из git log.
type Commit struct {
	Hash    string
	Author  string
	Subject string
	Body    string
}

// Change — разобранный коммит.
type Change struct {
	Commit
	Type     string
	Scope    string
	Desc     string
	Breaking bool
	// BreakingNote — текст после «BREAKING CHANGE:», если есть.
	BreakingNote string
}

// Level — насколько поднимать версию.
type Level int

const (
	None Level = iota
	Patch
	Minor
	Major
)

var headerRe = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^()]*)\))?(!)?:\s*(.+)$`)

// Parse разбирает коммит. ok=false, если заголовок не по Conventional Commits.
func Parse(c Commit) (Change, bool) {
	m := headerRe.FindStringSubmatch(strings.TrimSpace(c.Subject))
	if m == nil {
		return Change{Commit: c}, false
	}
	ch := Change{
		Commit:   c,
		Type:     strings.ToLower(m[1]),
		Scope:    strings.TrimSpace(m[2]),
		Breaking: m[3] == "!",
		Desc:     strings.TrimSpace(m[4]),
	}
	for _, line := range strings.Split(c.Body, "\n") {
		for _, p := range []string{"BREAKING CHANGE:", "BREAKING-CHANGE:"} {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), p); ok {
				ch.Breaking = true
				ch.BreakingNote = strings.TrimSpace(rest)
			}
		}
	}
	return ch, true
}

// LevelOf — вклад одного изменения в версию.
func LevelOf(ch Change) Level {
	switch {
	case ch.Breaking:
		return Major
	case ch.Type == "feat":
		return Minor
	case ch.Type == "fix" || ch.Type == "perf":
		return Patch
	}
	return None
}

// Bump — максимальный уровень по всем изменениям.
func Bump(changes []Change) Level {
	l := None
	for _, ch := range changes {
		if x := LevelOf(ch); x > l {
			l = x
		}
	}
	return l
}

// InitialVersion — версия первого релиза.
const InitialVersion = "v0.1.0"

var semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Next считает следующий тег. last — последний тег («» — релизов не было).
// До 1.0 ломающее изменение поднимает минорную версию, а не мажорную.
// Пустая строка — выпускать нечего.
func Next(last string, l Level) (string, error) {
	if l == None {
		return "", nil
	}
	if last == "" {
		return InitialVersion, nil
	}
	m := semverRe.FindStringSubmatch(last)
	if m == nil {
		return "", fmt.Errorf("тег %q не похож на vX.Y.Z", last)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	switch {
	case l == Major && major > 0:
		major, minor, patch = major+1, 0, 0
	case l == Major || l == Minor:
		minor, patch = minor+1, 0
	default:
		patch++
	}
	return fmt.Sprintf("v%d.%d.%d", major, minor, patch), nil
}

// git запускает git в каталоге dir.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// LastTag — старший тег vX.Y.Z среди достижимых из ref. «» — тегов нет.
func LastTag(dir, ref string) (string, error) {
	out, err := git(dir, "tag", "--merged", ref, "--sort=-v:refname", "--list", "v*")
	if err != nil {
		return "", err
	}
	for _, t := range strings.Fields(out) {
		if semverRe.MatchString(t) {
			return t, nil
		}
	}
	return "", nil
}

// TagExists сообщает, есть ли тег локально.
func TagExists(dir, tag string) bool {
	_, err := git(dir, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag)
	return err == nil
}

// Log читает коммиты диапазона в порядке от старых к новым, без merge.
func Log(dir, from, to string) ([]Commit, error) {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	out, err := git(dir, "log", "--reverse", "--no-merges", "--format=%H%x1f%an%x1f%s%x1f%b%x1e", rng)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 4)
		if len(f) < 4 {
			return nil, errors.New("непонятный вывод git log")
		}
		commits = append(commits, Commit{Hash: f[0], Author: f[1], Subject: f[2], Body: strings.TrimSpace(f[3])})
	}
	return commits, nil
}

// ParseAll разбирает коммиты, пропуская не-conventional и коммиты ботов.
func ParseAll(commits []Commit) []Change {
	var out []Change
	for _, c := range commits {
		if strings.HasSuffix(c.Author, "[bot]") {
			continue
		}
		if ch, ok := Parse(c); ok {
			out = append(out, ch)
		}
	}
	return out
}

var scopeNames = map[string]string{
	"kamera": "камера",
	"obedki": "объедки",
	"voda":   "вода",
	"baiki":  "байки",
	"cli":    "CLI",
}

type section struct {
	title string
	types []string
	body  bool // печатать ли текст коммита под пунктом
}

var sections = []section{
	{"Новое", []string{"feat"}, true},
	{"Исправления", []string{"fix"}, true},
	{"Производительность", []string{"perf"}, true},
	{"Документация", []string{"docs"}, false},
	{"Сборка и CI", []string{"build", "ci"}, false},
}

// trailerRe — служебные строки в конце сообщения коммита.
var trailerRe = regexp.MustCompile(`^(Co-Authored-By|Co-authored-by|Signed-off-by|Claude-Session|Reviewed-by|Refs|BREAKING[ -]CHANGE):`)

// paragraphs склеивает перенесённые строки тела коммита в абзацы
// и убирает служебные строки.
func paragraphs(body string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case trailerRe.MatchString(line):
			flush()
		default:
			cur = append(cur, line)
		}
	}
	flush()
	return out
}

// escapeMD экранирует символы, которые Markdown принял бы за выделение:
// «__pycache__» иначе превратится в жирное «pycache».
var mdEscaper = strings.NewReplacer("_", "\\_", "*", "\\*")

func escapeMD(s string) string { return mdEscaper.Replace(s) }

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func item(repo string, ch Change) string {
	desc := escapeMD(ch.Desc)
	if ch.Scope != "" {
		name := ch.Scope
		if ru, ok := scopeNames[ch.Scope]; ok {
			name = ru
		}
		desc = "**" + name + ":** " + desc
	} else {
		desc = capitalize(desc)
	}
	short := ch.Hash
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("- %s ([%s](https://github.com/%s/commit/%s))", desc, short, repo, ch.Hash)
}

// Options — параметры заметок.
type Options struct {
	Repo string // owner/name
	Prev string // предыдущий тег, «» — первый релиз
	Tag  string // новый тег
}

// Notes собирает заметки к релизу в Markdown.
func Notes(changes []Change, o Options) string {
	var b strings.Builder
	if o.Prev == "" {
		fmt.Fprintf(&b, "Первый релиз balanda.\n\n")
	}
	var breaking []Change
	for _, ch := range changes {
		if ch.Breaking {
			breaking = append(breaking, ch)
		}
	}
	if len(breaking) > 0 {
		b.WriteString("### ⚠ Ломает совместимость\n\n")
		for _, ch := range breaking {
			line := item(o.Repo, ch)
			if ch.BreakingNote != "" {
				line += "\n\n  " + escapeMD(ch.BreakingNote)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	for _, s := range sections {
		var items []string
		for _, ch := range changes {
			if !contains(s.types, ch.Type) {
				continue
			}
			line := item(o.Repo, ch)
			if s.body {
				for _, p := range paragraphs(ch.Body) {
					line += "\n\n  " + escapeMD(p)
				}
			}
			items = append(items, line)
		}
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", s.title)
		for i, it := range items {
			if i > 0 && strings.Contains(it, "\n") {
				b.WriteString("\n")
			}
			b.WriteString(it + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("### Установка\n\n")
	b.WriteString("```sh\n")
	fmt.Fprintf(&b, "curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | BALANDA_VERSION=%s sh\n", o.Repo, o.Tag)
	b.WriteString("```\n\n")
	b.WriteString("```powershell\n")
	fmt.Fprintf(&b, "$env:BALANDA_VERSION = '%s'; irm https://raw.githubusercontent.com/%s/main/install.ps1 | iex\n", o.Tag, o.Repo)
	b.WriteString("```\n\n")
	b.WriteString("Пакеты .deb, .rpm и .apk, архивы и `checksums.txt` — в файлах ниже.\n")
	if o.Prev != "" {
		fmt.Fprintf(&b, "\nВсе изменения: https://github.com/%s/compare/%s...%s\n", o.Repo, o.Prev, o.Tag)
	}
	return b.String()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

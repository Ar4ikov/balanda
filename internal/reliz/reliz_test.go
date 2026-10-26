package reliz

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		subject, body string
		ok            bool
		typ, scope    string
		breaking      bool
		desc          string
	}{
		{"feat(kamera): лимит на процессы", "", true, "feat", "kamera", false, "лимит на процессы"},
		{"fix: опечатка", "", true, "fix", "", false, "опечатка"},
		{"feat!: новый формат пайки", "", true, "feat", "", true, "новый формат пайки"},
		{"refactor(cli)!: убран флаг", "", true, "refactor", "cli", true, "убран флаг"},
		{"fix: x", "Текст.\n\nBREAKING CHANGE: --paika теперь обязателен", true, "fix", "", true, "x"},
		{"Merge pull request #1 from x", "", false, "", "", false, ""},
		{"обновил всё", "", false, "", "", false, ""},
		{"docs: перезаписаны GIF", "", true, "docs", "", false, "перезаписаны GIF"},
	}
	for _, tt := range tests {
		ch, ok := Parse(Commit{Subject: tt.subject, Body: tt.body})
		if ok != tt.ok {
			t.Errorf("%q: ok=%v", tt.subject, ok)
			continue
		}
		if !ok {
			continue
		}
		if ch.Type != tt.typ || ch.Scope != tt.scope || ch.Breaking != tt.breaking || ch.Desc != tt.desc {
			t.Errorf("%q: %+v", tt.subject, ch)
		}
	}
	ch, _ := Parse(Commit{Subject: "fix: x", Body: "BREAKING CHANGE: --paika теперь обязателен"})
	if ch.BreakingNote != "--paika теперь обязателен" {
		t.Errorf("BreakingNote = %q", ch.BreakingNote)
	}
	// Сноска на нескольких строках кончается на следующей сноске.
	ch, _ = Parse(Commit{Subject: "feat!: x", Body: "Тело.\n\nBREAKING CHANGE: в камере нет сети и права\nзаписи вне рабочего каталога.\n\nCo-Authored-By: X <x@y>"})
	if ch.BreakingNote != "в камере нет сети и права записи вне рабочего каталога." {
		t.Errorf("BreakingNote на двух строках = %q", ch.BreakingNote)
	}
	ch, _ = Parse(Commit{Subject: "feat!: x", Body: "BREAKING-CHANGE:\nтекст со следующей строки\nRefs: #12"})
	if ch.BreakingNote != "текст со следующей строки" {
		t.Errorf("BreakingNote со следующей строки = %q", ch.BreakingNote)
	}
}

func TestBump(t *testing.T) {
	mk := func(subjects ...string) []Change {
		var out []Change
		for _, s := range subjects {
			ch, _ := Parse(Commit{Subject: s})
			out = append(out, ch)
		}
		return out
	}
	tests := []struct {
		changes []Change
		want    Level
	}{
		{mk("docs: a", "ci: b", "chore: c", "test: d"), None},
		{mk("docs: a", "fix: b"), Patch},
		{mk("perf: a"), Patch},
		{mk("fix: a", "feat: b"), Minor},
		{mk("feat: a", "fix!: b"), Major},
		{nil, None},
	}
	for i, tt := range tests {
		if got := Bump(tt.changes); got != tt.want {
			t.Errorf("%d: Bump = %v, ждали %v", i, got, tt.want)
		}
	}
}

func TestNext(t *testing.T) {
	tests := []struct {
		last  string
		level Level
		want  string
	}{
		{"", None, ""},
		{"", Patch, "v0.1.0"},
		{"", Major, "v0.1.0"},
		{"v0.1.0", None, ""},
		{"v0.1.0", Patch, "v0.1.1"},
		{"v0.1.3", Minor, "v0.2.0"},
		{"v0.4.2", Major, "v0.5.0"}, // до 1.0 ломающее изменение поднимает минорную
		{"v1.4.2", Major, "v2.0.0"},
		{"v1.4.2", Minor, "v1.5.0"},
		{"v1.4.2", Patch, "v1.4.3"},
	}
	for _, tt := range tests {
		got, err := Next(tt.last, tt.level)
		if err != nil || got != tt.want {
			t.Errorf("Next(%q, %v) = %q, %v; ждали %q", tt.last, tt.level, got, err, tt.want)
		}
	}
	if _, err := Next("release-1", Patch); err == nil {
		t.Error("кривой тег: ждали ошибку")
	}
}

func TestParagraphs(t *testing.T) {
	body := "Первая строка\nпродолжение.\n\nВторой абзац.\n\nCo-Authored-By: X <x@y>\nClaude-Session: https://example.com"
	got := paragraphs(body)
	if len(got) != 2 || got[0] != "Первая строка продолжение." || got[1] != "Второй абзац." {
		t.Fatalf("paragraphs = %q", got)
	}
	// Продолжение сноски BREAKING CHANGE в тело не попадает.
	got = paragraphs("Тело.\n\nBREAKING CHANGE: нет сети и права\nзаписи.\n\nCo-Authored-By: X <x@y>")
	if len(got) != 1 || got[0] != "Тело." {
		t.Fatalf("paragraphs со сноской = %q", got)
	}
}

func TestNotes(t *testing.T) {
	var changes []Change
	for _, c := range []Commit{
		{Hash: "aaaaaaaaaaaa", Subject: "feat(kamera): лимит на процессы", Body: "Ищет __pycache__ и *.pyc.\n\nCo-Authored-By: X <x@y>"},
		{Hash: "bbbbbbbbbbbb", Subject: "fix: опечатка в справке"},
		{Hash: "cccccccccccc", Subject: "docs: README"},
		{Hash: "dddddddddddd", Subject: "ci: кэш"},
		{Hash: "eeeeeeeeeeee", Subject: "chore: мелочь"},
		{Hash: "ffffffffffff", Subject: "feat!: новый формат", Body: "BREAKING CHANGE: старый формат не читается"},
	} {
		ch, _ := Parse(c)
		changes = append(changes, ch)
	}
	n := Notes(changes, Options{Repo: "o/r", Prev: "v0.1.0", Tag: "v0.2.0"})
	for _, want := range []string{
		"### ⚠ Ломает совместимость",
		"Старый формат не читается",
		"### Новое",
		"- **камера:** лимит на процессы ([aaaaaaa](https://github.com/o/r/commit/aaaaaaaaaaaa))",
		`Ищет \_\_pycache\_\_ и \*.pyc.`,
		"### Исправления",
		"- Опечатка в справке",
		"### Документация",
		"### Сборка и CI",
		"BALANDA_VERSION=v0.2.0",
		"https://github.com/o/r/compare/v0.1.0...v0.2.0",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("в заметках нет %q:\n%s", want, n)
		}
	}
	for _, bad := range []string{"мелочь", "Co-Authored-By", "Первый релиз"} {
		if strings.Contains(n, bad) {
			t.Errorf("в заметках лишнее %q", bad)
		}
	}
	if !strings.Contains(Notes(nil, Options{Repo: "o/r", Tag: "v0.1.0"}), "Первый релиз") {
		t.Error("первый релиз не отмечен")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	f := filepath.Join(dir, "f.txt")
	data, _ := os.ReadFile(f)
	if err := os.WriteFile(f, append(data, msg...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

func TestGitHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "chore: начало")
	if tag, _ := LastTag(dir, "HEAD"); tag != "" {
		t.Fatalf("тегов нет, а LastTag = %q", tag)
	}
	commit(t, dir, "feat: первая возможность\n\nОписание.")
	commits, err := Log(dir, "", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Subject != "chore: начало" || commits[1].Body != "Описание." {
		t.Fatalf("Log: %+v", commits)
	}
	next, _ := Next("", Bump(ParseAll(commits)))
	if next != "v0.1.0" {
		t.Fatalf("первый релиз %q", next)
	}
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	gitRun(t, dir, "tag", "not-semver")
	commit(t, dir, "docs: опечатка")
	commit(t, dir, "fix: ошибка")

	last, err := LastTag(dir, "HEAD")
	if err != nil || last != "v0.1.0" {
		t.Fatalf("LastTag = %q, %v", last, err)
	}
	commits, _ = Log(dir, last, "HEAD")
	if len(commits) != 2 {
		t.Fatalf("после тега коммитов %d", len(commits))
	}
	next, _ = Next(last, Bump(ParseAll(commits)))
	if next != "v0.1.1" {
		t.Fatalf("следующий %q", next)
	}
	if !TagExists(dir, "v0.1.0") || TagExists(dir, "v9.9.9") {
		t.Fatal("TagExists ошибается")
	}
	// Предыдущий тег для уже поставленного тега ищется от его родителя.
	gitRun(t, dir, "tag", "-a", "v0.1.1", "-m", "v0.1.1")
	if prev, _ := LastTag(dir, "v0.1.1^"); prev != "v0.1.0" {
		t.Fatalf("предыдущий для v0.1.1: %q", prev)
	}
	// Коммиты ботов в заметки не попадают.
	if got := ParseAll([]Commit{{Author: "github-actions[bot]", Subject: "docs: перезаписаны GIF"}}); len(got) != 0 {
		t.Fatal("коммит бота попал в изменения")
	}
}

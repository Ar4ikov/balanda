package obedki

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func mk(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func found(res *Result) map[string]Kind {
	m := map[string]Kind{}
	for _, it := range res.Items {
		if it.Kind == KindGitBranches {
			continue
		}
		rel, _ := filepath.Rel(res.Root, it.Path)
		m[filepath.ToSlash(rel)] = it.Kind
	}
	return m
}

func TestScanHeuristics(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{
		"web/package.json":                       "{}",
		"web/node_modules/left-pad/index.js":     strings.Repeat("x", 1000),
		"web/node_modules/a/node_modules/b/i.js": "y",
		"web/.next/cache/x":                      "z",
		"py/app/__pycache__/m.cpython-312.pyc":   "c",
		"py/.venv/pyvenv.cfg":                    "home = /usr",
		"py/.venv/lib/site.py":                   "s",
		"py/env/readme.txt":                      "просто каталог env без pyvenv.cfg",
		"py/.pytest_cache/v/cache":               "p",
		"rust/Cargo.toml":                        "[package]",
		"rust/target/debug/app":                  "bin",
		"java/pom.xml":                           "<project/>",
		"java/target/classes/A.class":            "cls",
		"plain/target/keep.txt":                  "target без Cargo.toml и pom.xml",
		"plain/build/keep.txt":                   "build без Gradle",
		"plain/.next/keep.txt":                   ".next без package.json",
		"gradle/build.gradle.kts":                "",
		"gradle/build/out.jar":                   "jar",
		"gradle/.gradle/8.0/x":                   "g",
		"zig/build.zig":                          "",
		"zig/.zig-cache/o/x":                     "z",
		"repo/.git/objects/node_modules/x":       "внутри .git не ищем",
	})
	res, err := Scan(context.Background(), Options{Root: root, NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	got := found(res)
	want := map[string]Kind{
		"web/node_modules":   KindNodeModules,
		"web/.next":          KindJSCache,
		"py/app/__pycache__": KindPycache,
		"py/.venv":           KindVenv,
		"py/.pytest_cache":   KindPyCache,
		"rust/target":        KindRustTarget,
		"java/target":        KindMavenTarget,
		"gradle/build":       KindGradle,
		"gradle/.gradle":     KindGradle,
		"zig/.zig-cache":     KindZigCache,
	}
	for p, k := range want {
		if got[p] != k {
			t.Errorf("%s: нашли %q, ждали %q", p, got[p], k)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("лишняя находка: %s (%s)", p, got[p])
		}
	}
	for _, it := range res.Items {
		if it.Size <= 0 {
			t.Errorf("%s: размер %d", it.Path, it.Size)
		}
	}
	if res.Items[0].Kind != KindNodeModules {
		t.Errorf("первым должен идти самый большой — node_modules, а не %s", res.Items[0].Kind)
	}
}

func TestScanMaxDepth(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{
		"a/node_modules/x":     "1",
		"a/b/c/node_modules/x": "2",
	})
	res, err := Scan(context.Background(), Options{Root: root, NoGit: true, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := found(res)
	if _, ok := got["a/node_modules"]; !ok || len(got) != 1 {
		t.Fatalf("с глубиной 2 нашли %v", got)
	}
}

func TestWithin(t *testing.T) {
	root := filepath.FromSlash("/home/u/proj")
	tests := map[string]bool{
		"/home/u/proj/a":        true,
		"/home/u/proj/a/b":      true,
		"/home/u/proj":          false,
		"/home/u/projX/a":       false,
		"/home/u":               false,
		"/home/u/proj/../other": false,
		"/etc":                  false,
	}
	for p, want := range tests {
		if got := Within(root, filepath.FromSlash(p)); got != want {
			t.Errorf("Within(%s) = %v, ждали %v", p, got, want)
		}
	}
}

func TestCleanDryRunAndReal(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{
		"node_modules/x/index.js": "x",
		"src/main.js":             "код",
		"__pycache__/m.pyc":       "c",
	})
	res, err := Scan(context.Background(), Options{Root: root, NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("нашли %d, ждали 2", len(res.Items))
	}
	_, failed := Clean(context.Background(), res.Items, CleanOptions{Root: root, DryRun: true}, func(Outcome) {})
	if failed != 0 {
		t.Fatal("пробный прогон с ошибками")
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
		t.Fatal("пробный прогон что-то удалил")
	}
	freed, failed := Clean(context.Background(), res.Items, CleanOptions{Root: root}, func(Outcome) {})
	if failed != 0 || freed <= 0 {
		t.Fatalf("освобождено %d, ошибок %d", freed, failed)
	}
	for _, p := range []string{"node_modules", "__pycache__"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s не удалён", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "src", "main.js")); err != nil {
		t.Error("удалён исходник")
	}
}

func TestCleanRefusesOutside(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	mk(t, other, map[string]string{"node_modules/x": "чужое"})
	items := []Item{
		{Kind: KindNodeModules, Path: filepath.Join(other, "node_modules"), Size: 1},
		{Kind: KindNodeModules, Path: root, Size: 1},
		{Kind: KindNodeModules, Path: filepath.Join(root, "..", filepath.Base(other), "node_modules"), Size: 1},
		{Kind: KindToolCache, Path: filepath.Join(other, "node_modules"), Outside: true, Size: 1},
		{Kind: KindDocker, Outside: true},
	}
	var errs int
	_, failed := Clean(context.Background(), items, CleanOptions{Root: root}, func(o Outcome) {
		if o.Err != nil {
			errs++
		}
	})
	if failed != len(items) || errs != len(items) {
		t.Fatalf("отказов %d из %d", failed, len(items))
	}
	if _, err := os.Stat(filepath.Join(other, "node_modules", "x")); err != nil {
		t.Fatal("удалили файл вне корня")
	}
	// Даже с --keshi чужой путь не совпадает с известным кэшем.
	_, failed = Clean(context.Background(), items[3:4], CleanOptions{Root: root, Caches: true}, func(Outcome) {})
	if failed != 1 {
		t.Fatal("удалили «кэш», которого нет в списке известных")
	}
}

func TestCleanSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	mk(t, other, map[string]string{"node_modules/x": "чужое"})
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Skip("символические ссылки недоступны:", err)
	}
	// Сканер по ссылкам не ходит.
	res, err := Scan(context.Background(), Options{Root: root, NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("сканер ушёл по ссылке: %v", found(res))
	}
	// А подсунутый путь через ссылку отклоняется при удалении.
	it := Item{Kind: KindNodeModules, Path: filepath.Join(root, "link", "node_modules"), Size: 1}
	_, failed := Clean(context.Background(), []Item{it}, CleanOptions{Root: root}, func(Outcome) {})
	if failed != 1 {
		t.Fatal("удалили каталог через символическую ссылку")
	}
	if _, err := os.Stat(filepath.Join(other, "node_modules", "x")); err != nil {
		t.Fatal("файл за ссылкой удалён")
	}
}

func TestReadOnlyRemove(t *testing.T) {
	root := t.TempDir()
	mk(t, root, map[string]string{"__pycache__/sub/f.pyc": "x"})
	sub := filepath.Join(root, "__pycache__", "sub")
	if err := os.Chmod(filepath.Join(sub, "f.pyc"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := removeAll(filepath.Join(root, "__pycache__")); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMergedBranches(t *testing.T) {
	if !haveGit() {
		t.Skip("нет git")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	mk(t, repo, map[string]string{"a.txt": "a"})
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "a")
	gitRun(t, repo, "branch", "slita")
	gitRun(t, repo, "branch", "tozhe-slita")
	gitRun(t, repo, "checkout", "-q", "-b", "ne-slita")
	mk(t, repo, map[string]string{"b.txt": "b"})
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "b")
	gitRun(t, repo, "checkout", "-q", "main")

	res, err := Scan(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var it *Item
	for i := range res.Items {
		if res.Items[i].Kind == KindGitBranches {
			it = &res.Items[i]
		}
	}
	if it == nil {
		t.Fatalf("слитые ветки не найдены, предупреждения: %v", res.Warnings)
	}
	sort.Strings(it.Branches)
	if strings.Join(it.Branches, ",") != "slita,tozhe-slita" || it.Base != "main" {
		t.Fatalf("ветки %v в %s", it.Branches, it.Base)
	}
	_, failed := Clean(context.Background(), []Item{*it}, CleanOptions{Root: root}, func(o Outcome) {
		if o.Err != nil {
			t.Log(o.Err)
		}
	})
	if failed != 0 {
		t.Fatal("ветки не удалились")
	}
	again, err := mergedBranches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("остались слитые ветки: %v", again.Branches)
	}
	if !branchExists(context.Background(), repo, "ne-slita") {
		t.Fatal("удалена неслитая ветка")
	}
}

func TestParseDockerSize(t *testing.T) {
	tests := map[string]int64{
		"1.5GB":  1_500_000_000,
		"850MB":  850_000_000,
		"12.3kB": 12_300,
		"0B":     0,
		"мусор":  0,
	}
	for in, want := range tests {
		if got := ParseDockerSize(in); got != want {
			t.Errorf("ParseDockerSize(%q) = %d, ждали %d", in, got, want)
		}
	}
}

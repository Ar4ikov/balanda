// Package obedki ищет и удаляет мусор: каталоги зависимостей и сборок,
// кэши инструментов, слитые git-ветки, висячие Docker-образы.
package obedki

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Kind — вид объедков.
type Kind string

const (
	KindNodeModules Kind = "node_modules"
	KindPycache     Kind = "pycache"
	KindVenv        Kind = "venv"
	KindRustTarget  Kind = "rust-target"
	KindMavenTarget Kind = "maven-target"
	KindGradle      Kind = "gradle"
	KindJSCache     Kind = "js-cache"
	KindPyCache     Kind = "py-tool-cache"
	KindZigCache    Kind = "zig-cache"
	KindGitBranches Kind = "git-branches"
	KindToolCache   Kind = "tool-cache"
	KindDocker      Kind = "docker"
)

// Item — одна находка.
type Item struct {
	Kind  Kind
	Title string
	// Path — абсолютный путь. Для git — путь к репозиторию.
	Path string
	// Size в байтах, -1 если не считается (ветки).
	Size int64
	// Outside — лежит вне корня поиска (известный кэш, Docker).
	Outside bool
	// Branches — слитые ветки для KindGitBranches.
	Branches []string
	// Base — ветка, в которую они влиты.
	Base string
	// Count — число образов для Docker.
	Count int
	// Tool — для кэшей: чем чистить (go, npm, pip, cargo).
	Tool string
}

// Options — что искать.
type Options struct {
	Root string
	// Caches — известные кэши pip, npm, go и cargo вне корня.
	Caches bool
	// Docker — висячие образы.
	Docker bool
	// NoGit — не искать слитые ветки.
	NoGit bool
	// MaxDepth — глубина обхода, 0 — без ограничения.
	MaxDepth int
}

// Result — итог поиска.
type Result struct {
	Root     string
	Items    []Item
	Warnings []string
}

// Total — сумма размеров.
func (r *Result) Total() int64 {
	var n int64
	for _, it := range r.Items {
		if it.Size > 0 {
			n += it.Size
		}
	}
	return n
}

// ResolveRoot превращает путь в абсолютный без символических ссылок.
func ResolveRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s — не каталог", root)
	}
	return real, nil
}

// rule описывает, как узнать объедки по имени каталога.
type rule struct {
	kind  Kind
	title string
	// names — имена каталога; пусто — любое имя.
	names []string
	// marker — файл, который должен лежать рядом (в родителе).
	markers []string
	// inside — файл, который должен лежать внутри.
	inside string
}

var rules = []rule{
	{kind: KindNodeModules, title: "node_modules", names: []string{"node_modules"}},
	{kind: KindPycache, title: "__pycache__", names: []string{"__pycache__"}},
	{kind: KindVenv, title: "виртуальное окружение Python", inside: "pyvenv.cfg"},
	{kind: KindRustTarget, title: "сборка Rust (target)", names: []string{"target"}, markers: []string{"Cargo.toml"}},
	{kind: KindMavenTarget, title: "сборка Maven (target)", names: []string{"target"}, markers: []string{"pom.xml"}},
	{kind: KindGradle, title: "кэш Gradle (.gradle)", names: []string{".gradle"}, markers: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}},
	{kind: KindGradle, title: "сборка Gradle (build)", names: []string{"build"}, markers: []string{"build.gradle", "build.gradle.kts"}},
	{kind: KindJSCache, title: "кэш сборки JS", names: []string{".next", ".nuxt", ".svelte-kit", ".parcel-cache", ".turbo", ".angular"}, markers: []string{"package.json"}},
	{kind: KindPyCache, title: "кэш инструментов Python", names: []string{".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".hypothesis"}},
	{kind: KindZigCache, title: "кэш Zig", names: []string{".zig-cache", "zig-cache"}, markers: []string{"build.zig"}},
}

// skipNames — куда не заходим: метаданные VCS и общие кэши, их смотрит
// только Caches.
var skipNames = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	".cache": true, ".npm": true, ".cargo": true, ".rustup": true,
	".Trash": true,
}

func match(dir, name string) *rule {
	for i := range rules {
		r := &rules[i]
		if len(r.names) > 0 && !contains(r.names, name) {
			continue
		}
		if r.inside != "" {
			if fi, err := os.Lstat(filepath.Join(dir, name, r.inside)); err != nil || !fi.Mode().IsRegular() {
				continue
			}
		}
		if len(r.markers) > 0 {
			ok := false
			for _, m := range r.markers {
				if _, err := os.Lstat(filepath.Join(dir, m)); err == nil {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Scan ищет объедки.
func Scan(ctx context.Context, o Options) (*Result, error) {
	root, err := ResolveRoot(o.Root)
	if err != nil {
		return nil, err
	}
	res := &Result{Root: root}
	home, _ := os.UserHomeDir()
	caches := knownCaches()
	skipPaths := map[string]bool{}
	for _, c := range caches {
		skipPaths[c.Path] = true
	}
	if runtime.GOOS == "linux" {
		for _, p := range []string{"/proc", "/sys", "/dev", "/run"} {
			skipPaths[p] = true
		}
	}
	var repos []string
	rootDepth := strings.Count(root, string(filepath.Separator))
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() || path == root {
			return nil
		}
		name := d.Name()
		if name == ".git" {
			repos = append(repos, filepath.Dir(path))
			return fs.SkipDir
		}
		if skipNames[name] || skipPaths[path] {
			return fs.SkipDir
		}
		parent := filepath.Dir(path)
		if home != "" && parent == home && (name == "Library" || name == "AppData") {
			return fs.SkipDir
		}
		if r := match(parent, name); r != nil {
			res.Items = append(res.Items, Item{Kind: r.kind, Title: r.title, Path: path, Size: -1})
			return fs.SkipDir
		}
		if o.MaxDepth > 0 && strings.Count(path, string(filepath.Separator))-rootDepth >= o.MaxDepth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Корень сам может быть репозиторием.
	if fi, err := os.Lstat(filepath.Join(root, ".git")); err == nil && fi.IsDir() && !contains(repos, root) {
		repos = append(repos, root)
	}

	if !o.NoGit && len(repos) > 0 {
		if !haveGit() {
			res.Warnings = append(res.Warnings, "git не найден, слитые ветки не проверены")
		} else {
			for _, repo := range repos {
				it, err := mergedBranches(ctx, repo)
				if err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", repo, err))
					continue
				}
				if it != nil {
					res.Items = append(res.Items, *it)
				}
			}
		}
	}
	if o.Caches {
		for _, c := range caches {
			if fi, err := os.Stat(c.Path); err == nil && fi.IsDir() {
				c.Size = -1
				res.Items = append(res.Items, c)
			}
		}
	}
	measure(ctx, res.Items)
	if o.Docker {
		it, warn := dockerDangling(ctx)
		if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
		if it != nil {
			res.Items = append(res.Items, *it)
		}
	}
	sort.SliceStable(res.Items, func(i, j int) bool { return res.Items[i].Size > res.Items[j].Size })
	return res, nil
}

// measure считает размеры каталогов параллельно.
func measure(ctx context.Context, items []Item) {
	sem := make(chan struct{}, max(2, runtime.NumCPU()))
	var wg sync.WaitGroup
	for i := range items {
		if items[i].Kind == KindGitBranches || items[i].Kind == KindDocker {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *Item) {
			defer wg.Done()
			defer func() { <-sem }()
			it.Size = DirSize(ctx, it.Path)
		}(&items[i])
	}
	wg.Wait()
}

// DirSize — сумма размеров файлов в каталоге без перехода по ссылкам.
func DirSize(ctx context.Context, dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// Within проверяет, что path лежит строго внутри root.
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == "" {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Display — путь для вывода: относительно корня или с ~ вместо дома.
func Display(root string, it Item) string {
	if !it.Outside {
		if rel, err := filepath.Rel(root, it.Path); err == nil {
			if rel == "." {
				return filepath.Base(root)
			}
			return rel
		}
	}
	if it.Path == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, it.Path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return it.Path
}

// ErrOutside — попытка удалить что-то вне корня.
var ErrOutside = errors.New("путь вне корня поиска, не трогаем")

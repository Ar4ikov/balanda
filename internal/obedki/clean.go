package obedki

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// CleanOptions — что разрешено удалять.
type CleanOptions struct {
	Root string
	// Caches разрешает чистить известные кэши вне корня.
	Caches bool
	// Docker разрешает docker image prune.
	Docker bool
	// DryRun — только показать.
	DryRun bool
}

// Outcome — что вышло с одной находкой.
type Outcome struct {
	Item  Item
	Freed int64
	Err   error
}

// Clean удаляет находки. Каталоги удаляются, только если лежат внутри
// корня; кэши вне корня — только если разрешены и совпадают с известными.
func Clean(ctx context.Context, items []Item, o CleanOptions, report func(Outcome)) (freed int64, failed int) {
	root, err := ResolveRoot(o.Root)
	if err != nil {
		for _, it := range items {
			report(Outcome{Item: it, Err: err})
		}
		return 0, len(items)
	}
	known := map[string]bool{}
	for _, c := range knownCaches() {
		known[c.Path] = true
	}
	for _, it := range items {
		if ctx.Err() != nil {
			report(Outcome{Item: it, Err: ctx.Err()})
			failed++
			continue
		}
		err := check(root, it, o, known)
		if err == nil && !o.DryRun {
			err = cleanOne(ctx, it)
		}
		out := Outcome{Item: it, Err: err}
		if err == nil && it.Size > 0 {
			out.Freed = it.Size
			freed += it.Size
		}
		if err != nil {
			failed++
		}
		report(out)
	}
	return freed, failed
}

// check — последняя проверка перед удалением.
func check(root string, it Item, o CleanOptions, known map[string]bool) error {
	switch it.Kind {
	case KindDocker:
		if !o.Docker {
			return errors.New("образы Docker чистятся только с --docker")
		}
		return nil
	case KindToolCache:
		if !o.Caches {
			return errors.New("кэши вне каталога чистятся только с --keshi")
		}
		if !known[it.Path] {
			return ErrOutside
		}
		return nil
	case KindGitBranches:
		if it.Path != root && !Within(root, it.Path) {
			return ErrOutside
		}
		return nil
	}
	if it.Outside || !Within(root, it.Path) {
		return ErrOutside
	}
	fi, err := os.Lstat(it.Path)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s уже не каталог", it.Path)
	}
	// Родитель тоже не должен оказаться ссылкой, ведущей наружу.
	real, err := filepath.EvalSymlinks(filepath.Dir(it.Path))
	if err != nil {
		return err
	}
	if real != root && !Within(root, real) {
		return ErrOutside
	}
	return nil
}

func cleanOne(ctx context.Context, it Item) error {
	switch it.Kind {
	case KindGitBranches:
		return deleteBranches(ctx, it)
	case KindDocker:
		return runTool(ctx, "docker", "image", "prune", "-f")
	case KindToolCache:
		if it.Tool == "go" {
			if _, err := exec.LookPath("go"); err == nil {
				gocache, gomod := goEnv()
				switch it.Path {
				case gocache:
					return runTool(ctx, "go", "clean", "-cache")
				case gomod:
					return runTool(ctx, "go", "clean", "-modcache")
				}
			}
		}
		return removeAll(it.Path)
	}
	return removeAll(it.Path)
}

func runTool(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, firstLine(string(out)))
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// removeAll удаляет каталог. Кэш модулей Go и часть node_modules лежат
// только для чтения, поэтому при ошибке права сначала возвращаются.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		}
		_ = os.Chmod(p, mode)
		return nil
	})
	return os.RemoveAll(path)
}

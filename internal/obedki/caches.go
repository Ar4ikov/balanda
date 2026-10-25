package obedki

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// knownCaches возвращает кэши pip, npm, go и cargo, которые знает balanda.
// Существование каталогов не проверяется.
func knownCaches() []Item {
	home, _ := os.UserHomeDir()
	var out []Item
	add := func(tool, title, path string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		out = append(out, Item{Kind: KindToolCache, Title: title, Path: path, Outside: true, Tool: tool, Size: -1})
	}

	// pip
	pip := os.Getenv("PIP_CACHE_DIR")
	if pip == "" {
		switch runtime.GOOS {
		case "windows":
			pip = filepath.Join(os.Getenv("LOCALAPPDATA"), "pip", "Cache")
		case "darwin":
			pip = filepath.Join(home, "Library", "Caches", "pip")
		default:
			base := os.Getenv("XDG_CACHE_HOME")
			if base == "" && home != "" {
				base = filepath.Join(home, ".cache")
			}
			if base != "" {
				pip = filepath.Join(base, "pip")
			}
		}
	}
	add("pip", "кэш pip", pip)

	// npm: удаляем только _cacache, логи и настройки остаются.
	npm := os.Getenv("npm_config_cache")
	if npm == "" {
		if runtime.GOOS == "windows" {
			npm = filepath.Join(os.Getenv("LOCALAPPDATA"), "npm-cache")
		} else if home != "" {
			npm = filepath.Join(home, ".npm")
		}
	}
	if npm != "" {
		add("npm", "кэш npm", filepath.Join(npm, "_cacache"))
	}

	// go
	gocache, gomod := goEnv()
	add("go", "кэш сборки Go", gocache)
	add("go", "кэш модулей Go", gomod)

	// cargo
	cargo := os.Getenv("CARGO_HOME")
	if cargo == "" && home != "" {
		cargo = filepath.Join(home, ".cargo")
	}
	if cargo != "" {
		add("cargo", "кэш cargo (registry/cache)", filepath.Join(cargo, "registry", "cache"))
		add("cargo", "исходники cargo (registry/src)", filepath.Join(cargo, "registry", "src"))
		add("cargo", "git-зависимости cargo", filepath.Join(cargo, "git", "checkouts"))
	}
	return out
}

// goEnv узнаёт GOCACHE и GOMODCACHE: через `go env`, если go есть,
// иначе по умолчаниям.
func goEnv() (gocache, gomod string) {
	gocache, gomod = os.Getenv("GOCACHE"), os.Getenv("GOMODCACHE")
	if gocache != "" && gomod != "" {
		return gocache, gomod
	}
	if _, err := exec.LookPath("go"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "go", "env", "GOCACHE", "GOMODCACHE").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) == 2 {
				return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
			}
		}
	}
	if gocache == "" {
		if d, err := os.UserCacheDir(); err == nil {
			gocache = filepath.Join(d, "go-build")
		}
	}
	if gomod == "" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				gopath = filepath.Join(home, "go")
			}
		}
		if gopath != "" {
			gomod = filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "pkg", "mod")
		}
	}
	return gocache, gomod
}

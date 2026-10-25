// Package buildinfo хранит версию, коммит и дату сборки.
//
// GoReleaser подставляет значения через ldflags:
//
//	-X github.com/Ar4ikov/balanda/internal/buildinfo.Version=...
//
// При сборке через `go install` ldflags нет, тогда данные берутся
// из runtime/debug.ReadBuildInfo.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info описывает сборку.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Platform  string
}

// Get собирает сведения о текущей сборке.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "неизвестен"
	}
	if info.Date == "" {
		info.Date = "неизвестна"
	}
	return info
}

// Short возвращает только версию.
func Short() string {
	return Get().Version
}

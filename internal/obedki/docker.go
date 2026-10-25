package obedki

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// dockerDangling считает висячие образы (без тега). Если docker нет
// или демон не отвечает, возвращает предупреждение.
func dockerDangling(ctx context.Context) (*Item, string) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, "docker не найден, образы не проверены"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "images", "--filter", "dangling=true", "--format", "{{.ID}}\t{{.Size}}").Output()
	if err != nil {
		return nil, "docker есть, но демон не ответил: образы не проверены"
	}
	seen := map[string]bool{}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, size, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		total += ParseDockerSize(size)
	}
	if len(seen) == 0 {
		return nil, ""
	}
	return &Item{
		Kind:    KindDocker,
		Title:   "висячие Docker-образы",
		Size:    total,
		Count:   len(seen),
		Outside: true,
		Tool:    "docker",
	}, ""
}

// ParseDockerSize разбирает размер из вывода docker: «1.2GB», «850MB»,
// «12.3kB». Docker считает в десятичных единицах.
func ParseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suf  string
		mult float64
	}{
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suf); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil {
				return 0
			}
			return int64(f * u.mult)
		}
	}
	return 0
}

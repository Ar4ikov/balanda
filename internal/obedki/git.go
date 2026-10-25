package obedki

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func haveGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func git(ctx context.Context, repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func branchExists(ctx context.Context, repo, b string) bool {
	_, err := git(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+b)
	return err == nil
}

// baseBranch ищет основную ветку: то, на что указывает origin/HEAD,
// иначе main, master или trunk, если есть локально.
func baseBranch(ctx context.Context, repo string) string {
	if ref, err := git(ctx, repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b, ok := strings.CutPrefix(ref, "origin/"); ok && branchExists(ctx, repo, b) {
			return b
		}
	}
	for _, b := range []string{"main", "master", "trunk"} {
		if branchExists(ctx, repo, b) {
			return b
		}
	}
	return ""
}

// mergedBranches находит локальные ветки, целиком влитые в основную.
// Основная и текущая ветки в список не попадают.
func mergedBranches(ctx context.Context, repo string) (*Item, error) {
	base := baseBranch(ctx, repo)
	if base == "" {
		return nil, nil
	}
	current, _ := git(ctx, repo, "branch", "--show-current")
	out, err := git(ctx, repo, "for-each-ref", "--merged=refs/heads/"+base, "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var merged []string
	for _, b := range strings.Split(out, "\n") {
		b = strings.TrimSpace(b)
		if b == "" || b == base || b == current {
			continue
		}
		merged = append(merged, b)
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return &Item{
		Kind:     KindGitBranches,
		Title:    "слитые git-ветки",
		Path:     repo,
		Size:     -1,
		Branches: merged,
		Base:     base,
	}, nil
}

// deleteBranches удаляет ветки, перед этим ещё раз проверив, что каждая
// целиком лежит в основной: так коммиты не теряются.
func deleteBranches(ctx context.Context, it Item) error {
	var failed []string
	for _, b := range it.Branches {
		if _, err := git(ctx, it.Path, "merge-base", "--is-ancestor", "refs/heads/"+b, "refs/heads/"+it.Base); err != nil {
			failed = append(failed, b+" (уже не влита в "+it.Base+")")
			continue
		}
		if _, err := git(ctx, it.Path, "branch", "-D", "--", b); err != nil {
			failed = append(failed, b+" ("+err.Error()+")")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("не удалены: %s", strings.Join(failed, ", "))
	}
	return nil
}

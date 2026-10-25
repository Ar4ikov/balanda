package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Ar4ikov/balanda/internal/obedki"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

const obedkiLong = `Что считается объедками:

  node_modules, __pycache__   по имени каталога
  виртуальные окружения       каталог с pyvenv.cfg (.venv, venv и любые другие)
  target                      рядом Cargo.toml (Rust) или pom.xml (Maven)
  build, .gradle              рядом build.gradle или settings.gradle
  .next, .nuxt, .svelte-kit,  рядом package.json
  .parcel-cache, .turbo,
  .angular
  .pytest_cache, .mypy_cache, по имени каталога
  .ruff_cache, .tox,
  .hypothesis
  .zig-cache                  рядом build.zig
  слитые git-ветки            локальные ветки, целиком влитые в main
                              (master, trunk); текущая не трогается

С --keshi добавляются кэши pip, npm (_cacache), go (сборка и модули)
и cargo (registry, git/checkouts). С --docker — висячие Docker-образы,
если docker установлен. Без этих флагов ничего за пределами
указанного каталога не смотрится и не удаляется.

По символическим ссылкам обход не ходит, в .git, .cache, .npm,
.cargo и .rustup не заходит.`

type obedkiFlags struct {
	caches, docker, noGit bool
	depth                 int
}

func (f *obedkiFlags) register(c *cobra.Command) {
	c.Flags().BoolVar(&f.caches, "keshi", false, "добавить кэши pip, npm, go и cargo из домашнего каталога")
	c.Flags().BoolVar(&f.docker, "docker", false, "добавить висячие Docker-образы")
	c.Flags().BoolVar(&f.noGit, "bez-git", false, "не искать слитые git-ветки")
	c.Flags().IntVar(&f.depth, "glubina", 0, "насколько глубоко спускаться, `N` уровней (0 — до дна)")
}

func scanWith(c *cobra.Command, args []string, f obedkiFlags) (*obedki.Result, error) {
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
	defer stop()
	res, err := obedki.Scan(ctx, obedki.Options{
		Root: root, Caches: f.caches, Docker: f.docker, NoGit: f.noGit, MaxDepth: f.depth,
	})
	if errors.Is(err, context.Canceled) {
		return nil, errors.New("поиск прерван")
	}
	return res, err
}

func printObedki(out io.Writer, p vyvod.Palette, res *obedki.Result) {
	t := vyvod.Table{Header: []string{"ЧТО", "ГДЕ", "РАЗМЕР"}, Right: map[int]bool{2: true}}
	width := vyvod.Width(100)
	for _, it := range res.Items {
		where := obedki.Display(res.Root, it)
		size := vyvod.Size(it.Size)
		switch it.Kind {
		case obedki.KindGitBranches:
			n := int64(len(it.Branches))
			where = fmt.Sprintf("%s: %d %s (%s), влиты в %s", where, n,
				vyvod.Plural(n, "ветка", "ветки", "веток"), strings.Join(it.Branches, ", "), it.Base)
		case obedki.KindDocker:
			n := int64(it.Count)
			where = fmt.Sprintf("%d %s", n, vyvod.Plural(n, "образ", "образа", "образов"))
		}
		t.Rows = append(t.Rows, []string{it.Title, vyvod.Truncate(where, max(30, width-45)), size})
	}
	t.Write(out, p)
}

func total(res *obedki.Result) string {
	n := int64(len(res.Items))
	return fmt.Sprintf("%d %s, %s", n, vyvod.Plural(n, "находка", "находки", "находок"), vyvod.Size(res.Total()))
}

func flagsLine(f obedkiFlags) string {
	var parts []string
	if f.caches {
		parts = append(parts, "--keshi")
	}
	if f.docker {
		parts = append(parts, "--docker")
	}
	if f.noGit {
		parts = append(parts, "--bez-git")
	}
	if f.depth > 0 {
		parts = append(parts, "--glubina "+strconv.Itoa(f.depth))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func newScanCmd() *cobra.Command {
	var f obedkiFlags
	c := &cobra.Command{
		Use:     "scan [флаги] [путь]",
		Aliases: []string{"obedki"},
		GroupID: groupObedki,
		Short:   "найти объедки и показать, сколько они весят",
		Long:    "Ищет мусор сборок и кэшей в каталоге (по умолчанию в текущем) и ничего не удаляет.\n\n" + obedkiLong,
		Example: `  balanda scan
  balanda scan ~/projects --keshi
  balanda scan . --docker --bez-git`,
		Args: maxArgs(1, "один путь"),
		RunE: func(c *cobra.Command, args []string) error {
			res, err := scanWith(c, args, f)
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			p := vyvod.Colors(out)
			for _, w := range res.Warnings {
				fmt.Fprintf(c.ErrOrStderr(), "%sbalanda: %s%s\n", p.Dim, w, p.Reset)
			}
			if len(res.Items) == 0 {
				fmt.Fprintf(out, "В %s объедков нет.\n", res.Root)
				if !f.caches {
					fmt.Fprintf(out, "%sКэши pip, npm, go и cargo смотрит флаг --keshi.%s\n", p.Dim, p.Reset)
				}
				return nil
			}
			fmt.Fprintf(out, "Объедки в %s:\n\n", res.Root)
			printObedki(out, p, res)
			fmt.Fprintf(out, "\n%sИтого: %s.%s\n", p.Bold, total(res), p.Reset)
			path := ""
			if len(args) == 1 {
				path = " " + shellQuote(args[0])
			}
			fmt.Fprintf(out, "%sСварить: balanda svarit%s%s%s\n", p.Dim, path, flagsLine(f), p.Reset)
			return nil
		},
	}
	f.register(c)
	return c
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$\\`;&|<>()*?[]#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newSvaritCmd() *cobra.Command {
	var (
		f      obedkiFlags
		dryRun bool
		yes    bool
	)
	c := &cobra.Command{
		Use:     "svarit [флаги] [путь]",
		Aliases: []string{"clean"},
		GroupID: groupObedki,
		Short:   "удалить найденные объедки",
		Long: `Ищет то же, что scan, показывает список и после подтверждения удаляет.
Каталоги удаляются, только если лежат внутри указанного пути. Ветки
удаляются через git branch -D после повторной проверки, что каждая
целиком влита в основную ветку. Кэш go чистится через go clean,
Docker-образы — через docker image prune.

` + obedkiLong,
		Example: `  balanda svarit --dry-run
  balanda svarit ~/projects
  balanda svarit . --keshi --yes`,
		Args: maxArgs(1, "один путь"),
		RunE: func(c *cobra.Command, args []string) error {
			res, err := scanWith(c, args, f)
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			errOut := c.ErrOrStderr()
			p := vyvod.Colors(out)
			for _, w := range res.Warnings {
				fmt.Fprintf(errOut, "%sbalanda: %s%s\n", p.Dim, w, p.Reset)
			}
			if len(res.Items) == 0 {
				fmt.Fprintf(out, "В %s объедков нет, варить нечего.\n", res.Root)
				return nil
			}
			printObedki(out, p, res)
			fmt.Fprintln(out)
			if dryRun {
				fmt.Fprintf(out, "Пробный прогон: %s. Ничего не удалено.\n", total(res))
				return nil
			}
			if !yes {
				if !stdinIsTerminal() {
					return errors.New("нужно подтверждение: запустите из терминала или добавьте --yes")
				}
				if !vyvod.Confirm(os.Stdin, out, "Сварить "+total(res)+"?") {
					fmt.Fprintln(out, "Отмена, ничего не удалено.")
					return nil
				}
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			done := 0
			freed, failed := obedki.Clean(ctx, res.Items, obedki.CleanOptions{
				Root: res.Root, Caches: f.caches, Docker: f.docker,
			}, func(o obedki.Outcome) {
				where := obedki.Display(res.Root, o.Item)
				if where == "" {
					where = o.Item.Title
				}
				if o.Err != nil {
					fmt.Fprintf(errOut, "%s✗%s %s: %v\n", p.Red, p.Reset, where, o.Err)
					return
				}
				done++
				if o.Item.Kind == obedki.KindGitBranches {
					where += ": " + strings.Join(o.Item.Branches, ", ")
				}
				size := ""
				if o.Freed > 0 {
					size = " " + p.Dim + vyvod.Size(o.Freed) + p.Reset
				}
				fmt.Fprintf(out, "%s✓%s %s%s\n", p.Green, p.Reset, where, size)
			})
			fmt.Fprintf(out, "\n%sСварено %d из %d, освобождено %s.%s\n", p.Bold, done, len(res.Items), vyvod.Size(freed), p.Reset)
			if failed > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	f.register(c)
	c.Flags().BoolVar(&dryRun, "dry-run", false, "только показать, ничего не удалять")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "не спрашивать подтверждение")
	return c
}

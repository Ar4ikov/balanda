// Команда reliz считает следующую версию и печатает заметки к релизу.
// Её запускает workflow Release:
//
//	go run ./tools/reliz next              следующий тег или пустая строка
//	go run ./tools/reliz notes --tag v0.2.0
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Ar4ikov/balanda/internal/reliz"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fs.String("dir", ".", "каталог репозитория")
	repo := fs.String("repo", envOr("GITHUB_REPOSITORY", "Ar4ikov/balanda"), "репозиторий owner/name")
	tag := fs.String("tag", "", "тег релиза (для notes)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "next":
		err = next(*dir)
	case "notes":
		err = notes(*dir, *repo, *tag)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "reliz:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "использование: reliz next | reliz notes --tag vX.Y.Z")
	os.Exit(2)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func next(dir string) error {
	last, err := reliz.LastTag(dir, "HEAD")
	if err != nil {
		return err
	}
	commits, err := reliz.Log(dir, last, "HEAD")
	if err != nil {
		return err
	}
	changes := reliz.ParseAll(commits)
	level := reliz.Bump(changes)
	tag, err := reliz.Next(last, level)
	if err != nil {
		return err
	}
	names := map[reliz.Level]string{reliz.None: "нет", reliz.Patch: "патч", reliz.Minor: "минорная", reliz.Major: "ломающая"}
	prev := last
	if prev == "" {
		prev = "нет"
	}
	fmt.Fprintf(os.Stderr, "последний тег: %s; коммитов: %d, из них по Conventional Commits: %d; подъём: %s; следующий тег: %s\n",
		prev, len(commits), len(changes), names[level], strings.TrimSpace(tag+" "))
	fmt.Println(tag)
	return nil
}

func notes(dir, repo, tag string) error {
	if tag == "" {
		return fmt.Errorf("нужен --tag")
	}
	base := "HEAD"
	ref := "HEAD"
	if reliz.TagExists(dir, tag) {
		base, ref = tag, tag+"^"
	}
	prev, err := reliz.LastTag(dir, ref)
	if err != nil {
		return err
	}
	commits, err := reliz.Log(dir, prev, base)
	if err != nil {
		return err
	}
	fmt.Print(reliz.Notes(reliz.ParseAll(commits), reliz.Options{Repo: repo, Prev: prev, Tag: tag}))
	return nil
}

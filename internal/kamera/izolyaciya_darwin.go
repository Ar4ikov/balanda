//go:build darwin

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// На macOS свой /tmp не смонтировать: камере выдаётся отдельный
// каталог, и TMPDIR указывает туда.
const needOwnTmp = true

const sandboxExec = "/usr/bin/sandbox-exec"

// isolateCmd на macOS ничего не меняет снаружи: стены строит sandbox-exec
// внутри __zek.
func isolateCmd(*exec.Cmd, Izolyaciya) error {
	if _, err := os.Stat(sandboxExec); err != nil {
		return errors.New(isolationHint(err))
	}
	return nil
}

func isolationHint(err error) string {
	return "изоляция недоступна: " + err.Error() +
		"\nНужен " + sandboxExec + " (есть во всех версиях macOS). Запустить без изоляции: balanda sadit --bez-izolyacii ..."
}

// sandboxProfile собирает профиль Seatbelt. Пути передаются параметрами
// -D, чтобы их не пришлось экранировать. Если правила пересекаются,
// действует последнее.
func sandboxProfile(o zekOpts) (string, []string) {
	var b strings.Builder
	var params []string
	param := func(name, val string) string {
		params = append(params, "-D", name+"="+val)
		return `(param "` + name + `")`
	}
	// Seatbelt сравнивает настоящие пути: /var/folders для него —
	// /private/var/folders. Каждый путь идёт в правило в обоих видах.
	subpaths := func(name, p string) []string {
		out := []string{"(subpath " + param(name, p) + ")"}
		if real := realPath(p); real != p {
			out = append(out, "(subpath "+param(name+"R", real)+")")
		}
		return out
	}
	b.WriteString("(version 1)\n(allow default)\n")
	if !o.set {
		b.WriteString("(deny network*)\n")
		b.WriteString(`(allow network-bind network-inbound (local ip "localhost:*"))` + "\n")
		b.WriteString(`(allow network-outbound (remote ip "localhost:*"))` + "\n")
		b.WriteString("(allow network-outbound (remote unix-socket))\n")
	}
	b.WriteString("(deny file-write*)\n")
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/dtracehelper") (regex #"^/dev/tty") (regex #"^/dev/fd/"))` + "\n")
	home := userHome()
	if o.bezDoma && home != "" {
		b.WriteString("(deny file-read* file-write* " + strings.Join(subpaths("HOME", home), " ") + ")\n")
	}
	var open []string
	for i, p := range append([]string{o.dir}, o.zapis...) {
		if p != "" {
			open = append(open, subpaths(fmt.Sprintf("W%d", i), p)...)
		}
	}
	if o.tmp != "" {
		open = append(open, subpaths("TMP", o.tmp)...)
	}
	if len(open) > 0 {
		b.WriteString("(allow file-read* file-write* " + strings.Join(open, " ") + ")\n")
	}
	// Секреты — последним правилом: оно сильнее рабочего каталога, даже
	// если тот — весь дом.
	var hidden []string
	for i, p := range secretPaths(home) {
		hidden = append(hidden, subpaths(fmt.Sprintf("S%d", i), p)...)
	}
	b.WriteString("(deny file-read* file-write* " + strings.Join(hidden, " ") + ")\n")
	// Сигналы — только своим.
	b.WriteString("(deny signal)\n(allow signal (target same-sandbox))\n")
	return b.String(), params
}

// realPath раскрывает символические ссылки в пути. Если файла нет,
// раскрывается ближайший существующий родитель: /var/run/docker.sock
// без Docker становится /private/var/run/docker.sock.
func realPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// zekRun на macOS: rlimit, затем exec в sandbox-exec с профилем.
func zekRun(o zekOpts) int {
	if err := applyRlimits(o); err != nil {
		fmt.Fprintln(os.Stderr, "balanda:", err)
		return ExitNoStart
	}
	path, argv, env := o.path, o.argv, os.Environ()
	if o.odinochka {
		profile, params := sandboxProfile(o)
		args := append([]string{"sandbox-exec", "-p", profile}, params...)
		if o.proverka {
			args = append(args, "/usr/bin/true")
		} else {
			args = append(args, o.path)
			if len(o.argv) > 1 {
				args = append(args, o.argv[1:]...)
			}
		}
		path, argv = sandboxExec, args
		env = cleanEnv(env)
		if o.tmp != "" {
			env = append(env, "TMPDIR="+o.tmp+"/")
		}
		if err := os.Chdir(o.dir); err != nil {
			fmt.Fprintln(os.Stderr, "balanda:", err)
			return ExitNoStart
		}
	}
	err := syscall.Exec(path, argv, env)
	fmt.Fprintf(os.Stderr, "balanda: не запустить %s: %v\n", path, err)
	return ExitNoStart
}

// ProbeIzolyaciya проверяет, что sandbox-exec принимает профиль камеры.
func ProbeIzolyaciya() error {
	dir, _ := os.Getwd()
	profile, params := sandboxProfile(zekOpts{odinochka: true, dir: dir})
	args := append([]string{"-p", profile}, params...)
	args = append(args, "/usr/bin/true")
	out, err := exec.Command(sandboxExec, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", isolationHint(err), err, strings.TrimSpace(string(out)))
	}
	return nil
}

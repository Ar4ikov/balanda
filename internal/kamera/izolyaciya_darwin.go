//go:build darwin

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	b.WriteString("(version 1)\n(allow default)\n")
	if !o.set {
		b.WriteString("(deny network*)\n")
		b.WriteString(`(allow network* (local ip "localhost:*"))` + "\n")
		b.WriteString(`(allow network* (remote ip "localhost:*"))` + "\n")
		b.WriteString("(allow network* (remote unix-socket))\n")
	}
	b.WriteString("(deny file-write*)\n")
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/dtracehelper") (regex #"^/dev/tty") (regex #"^/dev/fd/"))` + "\n")
	home := userHome()
	var hidden []string
	if o.bezDoma && home != "" {
		hidden = append(hidden, "(subpath "+param("HOME", home)+")")
	} else {
		for i, p := range secretPaths(home) {
			hidden = append(hidden, "(subpath "+param(fmt.Sprintf("S%d", i), p)+")")
		}
	}
	if len(hidden) > 0 {
		b.WriteString("(deny file-read* file-write* " + strings.Join(hidden, " ") + ")\n")
	}
	var open []string
	for i, p := range append([]string{o.dir}, o.zapis...) {
		if p != "" {
			open = append(open, "(subpath "+param(fmt.Sprintf("W%d", i), p)+")")
		}
	}
	if o.tmp != "" {
		open = append(open, "(subpath "+param("TMP", o.tmp)+")")
	}
	if len(open) > 0 {
		b.WriteString("(allow file-read* file-write* " + strings.Join(open, " ") + ")\n")
	}
	// Сигналы — только своим.
	b.WriteString("(deny signal)\n(allow signal (target same-sandbox))\n")
	return b.String(), params
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

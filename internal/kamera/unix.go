//go:build unix

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// fgTTY — процесс отдан терминалу, после него терминал надо вернуть.
var fgTTY bool

// setGroup кладёт процесс в отдельную группу: так гасится всё дерево.
// Если надзиратель сидит в терминале на переднем плане, группа процесса
// становится передней: процесс читает ввод и получает Ctrl+C.
func setGroup(cmd *exec.Cmd, fg bool) {
	fgTTY = false
	if fg && cmd.Stdin == os.Stdin && isForeground(int(os.Stdin.Fd())) {
		fgTTY = true
		cmd.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Ctty: int(os.Stdin.Fd())}
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func isForeground(fd int) bool {
	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return false
	}
	return pgrp == unix.Getpgrp()
}

// restoreTerminal возвращает терминал группе надзирателя.
func restoreTerminal() {
	if !fgTTY {
		return
	}
	fgTTY = false
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	_ = unix.IoctlSetPointerInt(int(os.Stdin.Fd()), unix.TIOCSPGRP, unix.Getpgrp())
}

// forwardSignals передаёт группе процесса сигналы, пришедшие надзирателю.
func forwardSignals(eng engine, fg bool) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-ch:
				if s, ok := sig.(syscall.Signal); ok {
					if pg, ok := eng.(interface{ pgid() int }); ok && pg.pgid() > 0 {
						_ = syscall.Kill(-pg.pgid(), s)
					}
				}
			}
		}
	}()
	stopGuard := guardStopped(eng, fg)
	return func() {
		stopGuard()
		signal.Stop(ch)
		close(done)
	}
}

// guardStopped следит, чтобы процесс на переднем плане не завис
// остановленным после Ctrl+Z: надзиратель не умеет передать остановку
// оболочке, поэтому процесс продолжается.
func guardStopped(eng engine, fg bool) func() {
	pg, ok := eng.(interface{ pgid() int })
	if !fg || !fgTTY || !ok {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				pid := pg.pgid()
				if pid > 0 && isStopped(pid) {
					fmt.Fprintln(os.Stderr, "\nbalanda: Ctrl+Z в камере не работает, процесс продолжен")
					_ = syscall.Kill(-pid, syscall.SIGCONT)
				}
			}
		}
	}()
	return func() { close(done) }
}

// detach отрывает фонового надзирателя от терминала.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// exitInfo превращает итог процесса в код выхода и имя сигнала.
func exitInfo(ps *os.ProcessState, waitErr error) (int, string) {
	if ps == nil {
		if waitErr != nil {
			return 1, ""
		}
		return 0, ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		sig := ws.Signal()
		return 128 + int(sig), signalName(sig)
	}
	return ps.ExitCode(), ""
}

func signalName(sig syscall.Signal) string {
	if n := unix.SignalName(sig); n != "" {
		return n
	}
	return "сигнал " + strconv.Itoa(int(sig))
}

// Alive проверяет, жив ли процесс. Если start не ноль, сверяет время
// старта, чтобы не спутать процесс с новым, получившим тот же PID.
func Alive(pid int, start uint64) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if isZombie(pid) {
		return false
	}
	if start != 0 {
		if cur := procStart(pid); cur != 0 && cur != start {
			return false
		}
	}
	return true
}

func groupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killGroup гасит группу процессов: SIGTERM, пауза, SIGKILL.
func killGroup(pgid int, graceful bool) {
	if pgid <= 1 {
		return
	}
	if graceful {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		deadline := time.Now().Add(killGrace)
		for time.Now().Before(deadline) {
			if !groupAlive(pgid) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// groupEngine — общее для движков, которые держат камеру группой
// процессов: rlimit и «без лимитов».
type groupEngine struct {
	pid    int
	limits bool
	p      Paika
}

func (g *groupEngine) pgid() int { return g.pid }

func (g *groupEngine) prepare(cmd *exec.Cmd) error {
	if !g.limits {
		return nil
	}
	self, err := selfExe()
	if err != nil {
		return fmt.Errorf("не найти свой бинарник: %w", err)
	}
	args := []string{self, argZek}
	if g.p.Mem > 0 && useRlimitAS {
		args = append(args, "--as", strconv.FormatInt(g.p.Mem, 10))
	}
	if s := g.p.CPUSeconds(); s > 0 {
		args = append(args, "--cpu", strconv.FormatUint(s, 10))
	}
	args = append(args, "--", cmd.Path)
	args = append(args, cmd.Args...)
	cmd.Path = self
	cmd.Args = args
	return nil
}

func (g *groupEngine) started(p *os.Process) error {
	g.pid = p.Pid
	return nil
}

func (g *groupEngine) kill(graceful bool) { killGroup(g.pid, graceful) }

func (g *groupEngine) finish(ps *os.ProcessState) (*Usage, string) {
	if ps == nil {
		return nil, ""
	}
	// Процесс вышел, остальных в группе добиваем: камера закрывается.
	killGroup(g.pid, false)
	u := rusage(ps)
	return u, cpuReason(g.p, ps, u)
}

// cpuReason узнаёт смерть от RLIMIT_CPU. Мягкий лимит шлёт SIGXCPU,
// но Go-программы и многие другие его игнорируют, и тогда через
// секунду приходит SIGKILL от жёсткого лимита.
func cpuReason(p Paika, ps *os.ProcessState, u *Usage) string {
	secs := p.CPUSeconds()
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if secs == 0 || !ok || !ws.Signaled() || u == nil {
		return ""
	}
	switch ws.Signal() {
	case syscall.SIGXCPU:
		return ReasonCPU
	case syscall.SIGKILL:
		if float64(u.UserMs+u.SysMs) >= float64(secs)*1000*0.9 {
			return ReasonCPU
		}
	}
	return ""
}

func rusage(ps *os.ProcessState) *Usage {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return nil
	}
	return &Usage{
		UserMs:  ru.Utime.Nano() / 1e6,
		SysMs:   ru.Stime.Nano() / 1e6,
		PeakMem: ru.Maxrss * maxrssUnit,
	}
}

func rlimitWarnings(p Paika) []string {
	var w []string
	if p.CPU > 0 && p.Time == 0 {
		w = append(w, "cpu без time не ограничивается: долю ядра умеют держать только cgroups v2")
	} else if p.CPU > 0 {
		w = append(w, fmt.Sprintf("cpu ограничен суммарно: %d с процессорного времени (cpu × time)", p.CPUSeconds()))
	}
	if p.Mem > 0 && !useRlimitAS {
		w = append(w, "память проверяется сторожем раз в 0,5 с: короткий всплеск успеет пройти")
	}
	if p.CPUSeconds() > 0 && runtime.GOOS == "darwin" {
		w = append(w, "процессорное время сверх лимита гасит сторож, проверка раз в 0,5 с")
	}
	return w
}

// runZek — режим «__zek»: поставить rlimit самому себе и превратиться
// в нужную команду через exec. PID при этом не меняется.
func runZek(args []string) int {
	var (
		as, cpu uint64
		rest    []string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--as", "--cpu":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "balanda: __zek: нет значения у", args[i])
				return ExitNoStart
			}
			v, err := strconv.ParseUint(args[i+1], 10, 64)
			if err != nil {
				fmt.Fprintln(os.Stderr, "balanda: __zek:", err)
				return ExitNoStart
			}
			if args[i] == "--as" {
				as = v
			} else {
				cpu = v
			}
			i++
		case "--":
			rest = args[i+1:]
			i = len(args)
		default:
			fmt.Fprintln(os.Stderr, "balanda: __zek: непонятный аргумент", args[i])
			return ExitNoStart
		}
	}
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "balanda: __zek: нет команды")
		return ExitNoStart
	}
	if as > 0 && rlimitAS >= 0 {
		if err := setrlimit(rlimitAS, as, as); err != nil {
			fmt.Fprintln(os.Stderr, "balanda: не поставить лимит памяти:", err)
			return ExitNoStart
		}
	}
	if cpu > 0 {
		// Мягкий лимит шлёт SIGXCPU, жёсткий на секунду позже — SIGKILL.
		if err := setrlimit(unix.RLIMIT_CPU, cpu, cpu+1); err != nil {
			fmt.Fprintln(os.Stderr, "balanda: не поставить лимит CPU:", err)
			return ExitNoStart
		}
	}
	err := syscall.Exec(rest[0], rest[1:], os.Environ())
	fmt.Fprintf(os.Stderr, "balanda: не запустить %s: %v\n", rest[1], err)
	return ExitNoStart
}

// setrlimit скрывает разницу в типах: на FreeBSD поля Rlimit — int64.
func setrlimit(res int, cur, max uint64) error {
	var rl unix.Rlimit
	setRlim(&rl.Cur, cur)
	setRlim(&rl.Max, max)
	return unix.Setrlimit(res, &rl)
}

func setRlim[T ~int64 | ~uint64](p *T, v uint64) { *p = T(v) }

// killKamera гасит камеру из другого процесса (otboy).
func killKamera(k *Kamera, graceful bool) error {
	if k.PGID <= 1 {
		return errors.New("у камеры нет группы процессов")
	}
	mainAlive := Alive(k.PID, k.ProcStart)
	if !mainAlive && !groupAlive(k.PGID) {
		return nil
	}
	if k.PID == k.PGID && !mainAlive && k.ProcStart != 0 && procStart(k.PID) != 0 {
		// Под этим PID уже другой процесс: группу не трогаем.
		return errors.New("процесс камеры уже сменился другим")
	}
	killGroup(k.PGID, graceful)
	killEngineRest(k)
	return nil
}

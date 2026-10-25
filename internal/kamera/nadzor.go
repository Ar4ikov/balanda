package kamera

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Служебные аргументы, с которыми balanda перезапускает сама себя.
const (
	argNadzor = "__nadziratel"
	argZek    = "__zek"
)

// selfExe — путь к бинарнику balanda. В тестах его подменяют.
var selfExe = os.Executable

// Коды выхода, как у coreutils timeout.
const (
	ExitTimeout  = 124
	ExitNoStart  = 127
	ExitKilled   = 137
	killGrace    = 2 * time.Second
	startTimeout = 10 * time.Second
)

// engine ставит лимиты и гасит камеру на конкретной платформе.
type engine interface {
	// prepare дописывает в команду всё нужное для лимитов.
	prepare(cmd *exec.Cmd) error
	// started вызывается сразу после Start.
	started(p *os.Process) error
	// kill гасит всю камеру: при graceful сначала просит по-хорошему.
	kill(graceful bool)
	// finish собирает итоговый расход, добивает оставшиеся процессы
	// и убирает за собой. reason — причина смерти, если её видно
	// по движку (например, OOM в cgroup).
	finish(ps *os.ProcessState) (u *Usage, reason string)
}

// Stats — живые данные о камере для shmon и pereklichka.
type Stats struct {
	CPUUser  time.Duration
	CPUSys   time.Duration
	CPUTotal time.Duration
	Mem      int64 // текущая память, -1 если неизвестно
	PeakMem  int64 // пик памяти, -1 если неизвестно
	Procs    []Proc
	Files    []string
	// FilesCount может быть больше len(Files): список обрезается.
	FilesCount int
	// FilesNote объясняет, почему файлов не видно.
	FilesNote string
	// Source — откуда взяты данные: /proc, cgroup, ps, Job Object.
	Source string
}

// Proc — процесс внутри камеры.
type Proc struct {
	PID int
	Cmd string
	Mem int64
}

// RunOptions — что и как запускать.
type RunOptions struct {
	Name       string
	Cmd        []string
	Paika      Paika
	Background bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	// Notify — куда писать сообщения о самой камере (обычно stderr).
	Notify io.Writer
}

// Sadit сажает процесс в камеру. В обычном режиме ждёт завершения
// и возвращает код выхода процесса. В фоновом режиме возвращает
// управление, как только процесс запущен.
func Sadit(s *Store, o RunOptions) (*Kamera, int, error) {
	if len(o.Cmd) == 0 {
		return nil, 0, errors.New("не указана команда")
	}
	dir, _ := os.Getwd()
	k := &Kamera{
		Name:       o.Name,
		Cmd:        o.Cmd,
		Dir:        dir,
		Paika:      o.Paika,
		Background: o.Background,
		Status:     StatusSidit,
		Started:    time.Now(),
	}
	if err := s.Reserve(k); err != nil {
		return nil, 0, err
	}
	if o.Background {
		return startBackground(s, k)
	}
	code := supervise(s, k, o.Stdin, o.Stdout, o.Stderr, o.Notify, true)
	return k, code, nil
}

// startBackground запускает отдельного надзирателя и ждёт, пока он
// посадит процесс.
func startBackground(s *Store, k *Kamera) (*Kamera, int, error) {
	k.Stdout, k.Stderr = s.LogPaths(k.ID)
	if err := s.Save(k); err != nil {
		return k, 0, err
	}
	self, err := selfExe()
	if err != nil {
		return k, 0, fmt.Errorf("не найти свой бинарник: %w", err)
	}
	logf, err := os.OpenFile(k.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return k, 0, err
	}
	defer logf.Close()
	cmd := exec.Command(self, argNadzor, s.Dir, strconv.Itoa(k.ID))
	cmd.Stdout = logf
	cmd.Stderr = logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		k.Status = StatusVyshla
		k.Error = "не запустился надзиратель: " + err.Error()
		_ = s.Save(k)
		return k, 0, errors.New(k.Error)
	}
	nadzorPID := cmd.Process.Pid
	_ = cmd.Process.Release()

	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		cur, err := s.Load(k.ID)
		if err != nil {
			continue
		}
		if cur.PID != 0 || cur.Status != StatusSidit {
			if cur.Error != "" {
				return cur, 0, errors.New(cur.Error)
			}
			return cur, 0, nil
		}
		if !Alive(nadzorPID, 0) {
			break
		}
	}
	cur, err := s.Load(k.ID)
	if err == nil && (cur.PID != 0 || cur.Error != "") {
		if cur.Error != "" {
			return cur, 0, errors.New(cur.Error)
		}
		return cur, 0, nil
	}
	return k, 0, fmt.Errorf("надзиратель не отчитался за %s, журнал: %s", startTimeout, k.Stderr)
}

// runNadzor — точка входа фонового надзирателя.
func runNadzor(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "balanda: надзирателю нужны каталог и номер камеры")
		return 2
	}
	s := &Store{Dir: args[0]}
	id, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "balanda: номер камеры должен быть числом")
		return 2
	}
	k, err := s.Load(id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "balanda:", err)
		return 2
	}
	out, err := os.OpenFile(k.Stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		k.Status, k.Error = StatusVyshla, err.Error()
		_ = s.Save(k)
		return 2
	}
	defer out.Close()
	errf, err := os.OpenFile(k.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		k.Status, k.Error = StatusVyshla, err.Error()
		_ = s.Save(k)
		return 2
	}
	defer errf.Close()
	return supervise(s, k, nil, out, errf, errf, false)
}

// supervise запускает процесс, держит лимит времени и записывает итог.
func supervise(s *Store, k *Kamera, stdin io.Reader, stdout, stderr, notify io.Writer, fg bool) int {
	if notify == nil {
		notify = io.Discard
	}
	k.Nadzor = os.Getpid()
	k.NadzorStart = procStart(k.Nadzor)

	eng, cmd, err := startCmd(k, stdin, stdout, stderr, fg)
	if err != nil {
		code := ExitNoStart
		now := time.Now()
		k.Status = StatusVyshla
		k.Error = "не удалось запустить: " + err.Error()
		k.ExitCode = &code
		k.Ended = &now
		_ = s.Save(k)
		fmt.Fprintf(notify, "balanda: %s: %s\n", k.Label(), k.Error)
		return code
	}
	k.PID = cmd.Process.Pid
	k.PGID = k.PID
	k.ProcStart = procStart(k.PID)
	if err := s.Save(k); err != nil {
		fmt.Fprintf(notify, "balanda: %v\n", err)
	}
	for _, w := range k.Warnings {
		fmt.Fprintf(notify, "balanda: %s: %s\n", k.Label(), w)
	}

	var (
		mu     sync.Mutex
		reason string
	)
	setReason := func(r string) {
		mu.Lock()
		if reason == "" {
			reason = r
		}
		mu.Unlock()
	}
	var timer *time.Timer
	if k.Paika.Time > 0 {
		timer = time.AfterFunc(k.Paika.Time, func() {
			setReason(ReasonTime)
			eng.kill(true)
		})
	}
	stopWatch := watchLimits(k, func(reason string) {
		setReason(reason)
		eng.kill(false)
	})
	stopSignals := forwardSignals(eng, fg)

	waitErr := cmd.Wait()
	if timer != nil {
		timer.Stop()
	}
	stopWatch()
	stopSignals()
	if fg {
		restoreTerminal()
	}

	usage, engReason := eng.finish(cmd.ProcessState)
	if engReason != "" {
		setReason(engReason)
	}
	now := time.Now()
	k.Ended = &now
	k.Usage = usage

	code, sig := exitInfo(cmd.ProcessState, waitErr)
	if sig == "SIGXCPU" {
		setReason(ReasonCPU)
	}
	k.Signal = sig
	mu.Lock()
	r := reason
	mu.Unlock()
	switch {
	case s.takeOtboy(k.ID):
		k.Status = StatusPogashena
		k.Reason = ReasonOtboy
	case r != "":
		k.Status = StatusUbita
		k.Reason = r
		if r == ReasonTime {
			code = ExitTimeout
		}
	default:
		k.Status = StatusVyshla
	}
	k.ExitCode = &code
	if err := s.Save(k); err != nil {
		fmt.Fprintf(notify, "balanda: %v\n", err)
	}
	if k.Status == StatusUbita && !fg {
		fmt.Fprintf(notify, "balanda: %s убита: %s\n", k.Label(), ReasonTitle(k.Reason))
	}
	return code
}

// startCmd выбирает движок и запускает процесс. Если движок не смог
// запустить процесс, пробует запасной (например, rlimit вместо cgroup).
func startCmd(k *Kamera, stdin io.Reader, stdout, stderr io.Writer, fg bool) (engine, *exec.Cmd, error) {
	engines, err := newEngines(k)
	if err != nil {
		return nil, nil, err
	}
	var lastErr error
	for i, mk := range engines {
		eng, name, info, err := mk()
		if err != nil {
			lastErr = err
			k.Warnings = append(k.Warnings, err.Error())
			continue
		}
		cmd := exec.Command(k.Cmd[0], k.Cmd[1:]...)
		if lp, err := exec.LookPath(k.Cmd[0]); err == nil {
			cmd.Path = lp
		} else if !errors.Is(err, exec.ErrDot) {
			return nil, nil, fmt.Errorf("команда %q не найдена", k.Cmd[0])
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
		setGroup(cmd, fg)
		if err := eng.prepare(cmd); err != nil {
			lastErr = err
			eng.finish(nil)
			if i < len(engines)-1 {
				k.Warnings = append(k.Warnings, fmt.Sprintf("%s не сработал: %v", EngineTitle(name), err))
				continue
			}
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			lastErr = err
			eng.finish(nil)
			if i < len(engines)-1 && !errors.Is(err, exec.ErrNotFound) {
				k.Warnings = append(k.Warnings, fmt.Sprintf("%s не сработал: %v", EngineTitle(name), err))
				continue
			}
			return nil, nil, err
		}
		if err := eng.started(cmd.Process); err != nil {
			eng.kill(false)
			_ = cmd.Wait()
			eng.finish(nil)
			lastErr = err
			if i < len(engines)-1 {
				k.Warnings = append(k.Warnings, fmt.Sprintf("%s не сработал: %v", EngineTitle(name), err))
				continue
			}
			return nil, nil, err
		}
		k.Engine, k.EngineInfo = name, info
		return eng, cmd, nil
	}
	if lastErr == nil {
		lastErr = errors.New("нет ни одного движка")
	}
	return nil, nil, lastErr
}

// engineMaker создаёт движок и возвращает его имя и описание.
type engineMaker func() (eng engine, name, info string, err error)

// Otboy гасит камеру: ставит метку и убивает процессы. Если надзиратель
// жив, он сам допишет итог. Если нет — итог пишет Otboy.
func Otboy(s *Store, k *Kamera) error {
	if k.Status != StatusSidit {
		return nil
	}
	if err := s.markOtboy(k.ID); err != nil {
		return err
	}
	nadzorAlive := Alive(k.Nadzor, k.NadzorStart)
	if err := killKamera(k, true); err != nil && !nadzorAlive {
		return err
	}
	if nadzorAlive {
		deadline := time.Now().Add(killGrace + 3*time.Second)
		for time.Now().Before(deadline) {
			cur, err := s.Load(k.ID)
			if err == nil && cur.Status != StatusSidit {
				*k = *cur
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	s.takeOtboy(k.ID)
	now := time.Now()
	k.Status = StatusPogashena
	k.Reason = ReasonOtboy
	k.Ended = &now
	return s.Save(k)
}

// Check — проверка камеры для переклички: живы ли процесс и надзиратель.
// Камеру, у которой пропали и надзиратель, и процесс, помечает
// потерянной.
func Check(s *Store, k *Kamera) (procAlive, nadzorAlive bool) {
	if k.Status != StatusSidit {
		return false, false
	}
	procAlive = k.PID != 0 && Alive(k.PID, k.ProcStart)
	nadzorAlive = Alive(k.Nadzor, k.NadzorStart)
	if !nadzorAlive && !procAlive {
		// Перечитываем: надзиратель мог успеть дописать итог.
		if cur, err := s.Load(k.ID); err == nil && cur.Status != StatusSidit {
			*k = *cur
			return false, false
		}
		if k.PID == 0 && time.Since(k.Started) < startTimeout {
			return false, false
		}
		now := time.Now()
		k.Status = StatusPoteryana
		k.Ended = &now
		_ = s.Save(k)
	}
	return procAlive, nadzorAlive
}

// LiveStats — текущие данные о камере.
func LiveStats(k *Kamera) (*Stats, error) {
	if k.Status != StatusSidit || k.PID == 0 {
		return nil, errors.New("камера не сидит")
	}
	return liveStats(k)
}

// Dispatch обрабатывает служебные режимы. Вызывается из main до cobra.
func Dispatch(args []string) (int, bool) {
	if len(args) < 2 {
		return 0, false
	}
	switch args[1] {
	case argNadzor:
		return runNadzor(args[2:]), true
	case argZek:
		return runZek(args[2:]), true
	}
	return 0, false
}

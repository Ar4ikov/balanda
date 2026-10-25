//go:build linux

package kamera

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	cgroupRoot = "/sys/fs/cgroup"
	// На Linux ru_maxrss в килобайтах.
	maxrssUnit = 1024
	// RLIMIT_AS на Linux работает.
	useRlimitAS = true
	maxFiles    = 50
)

var clockTicks = int64(100) // sysconf(_SC_CLK_TCK) почти везде 100

// newEngines выбирает движки по порядку: cgroups v2, затем rlimit.
// BALANDA_ENGINE=rlimit или cgroup2 оставляет только один.
func newEngines(k *Kamera) ([]engineMaker, error) {
	none := func() (engine, string, string, error) {
		return &groupEngine{}, EngineNone, "", nil
	}
	rl := func() (engine, string, string, error) {
		k.Warnings = append(k.Warnings, rlimitWarnings(k.Paika)...)
		return &groupEngine{limits: true, p: k.Paika}, EngineRlimit, "", nil
	}
	cg := func() (engine, string, string, error) {
		e, err := newCgroupEngine(k)
		if err != nil {
			return nil, "", "", err
		}
		return e, EngineCgroup, e.dir, nil
	}
	if k.Paika.Empty() {
		return []engineMaker{none}, nil
	}
	switch os.Getenv("BALANDA_ENGINE") {
	case EngineRlimit:
		return []engineMaker{rl}, nil
	case EngineCgroup:
		return []engineMaker{cg}, nil
	}
	if _, err := CgroupParent(); err != nil {
		return []engineMaker{rl}, nil
	}
	return []engineMaker{cg, rl}, nil
}

// EngineFor сообщает, какой движок достанется новой камере с лимитами.
func EngineFor() (string, string) {
	switch os.Getenv("BALANDA_ENGINE") {
	case EngineRlimit:
		return EngineRlimit, "выбрано через BALANDA_ENGINE"
	case EngineCgroup:
		return EngineCgroup, "выбрано через BALANDA_ENGINE"
	}
	parent, err := CgroupParent()
	if err != nil {
		return EngineRlimit, err.Error()
	}
	return EngineCgroup, parent
}

// CgroupParent ищет cgroup, в которой можно создавать камеры: ближайшего
// предка своей cgroup, где включены контроллеры cpu и memory и куда
// у пользователя есть право писать (делегированное поддерево).
func CgroupParent() (string, error) {
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return "", errors.New("cgroups v2 не смонтированы")
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	var own string
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			own = rest
		}
	}
	if own == "" {
		return "", errors.New("процесс не в cgroups v2")
	}
	dir := filepath.Join(cgroupRoot, own)
	for {
		parent := filepath.Dir(dir)
		if len(parent) < len(cgroupRoot) {
			break
		}
		if usableParent(parent) {
			return parent, nil
		}
		if parent == cgroupRoot {
			break
		}
		dir = parent
	}
	return "", errors.New("нет делегированной cgroup с контроллерами cpu и memory")
}

func usableParent(dir string) bool {
	ctl, err := os.ReadFile(filepath.Join(dir, "cgroup.subtree_control"))
	if err != nil {
		return false
	}
	have := map[string]bool{}
	for _, c := range strings.Fields(string(ctl)) {
		have[c] = true
	}
	if !have["cpu"] || !have["memory"] {
		return false
	}
	if unix.Access(dir, unix.W_OK) != nil {
		return false
	}
	return unix.Access(filepath.Join(dir, "cgroup.procs"), unix.W_OK) == nil
}

type cgroupEngine struct {
	dir string
	fd  *os.File
	pid int
	p   Paika
}

func newCgroupEngine(k *Kamera) (*cgroupEngine, error) {
	parent, err := CgroupParent()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(parent, fmt.Sprintf("balanda-%d-%d", k.ID, os.Getpid()))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, fmt.Errorf("не создать cgroup: %w", err)
	}
	e := &cgroupEngine{dir: dir, p: k.Paika}
	fail := func(err error) (*cgroupEngine, error) {
		_ = os.Remove(dir)
		return nil, err
	}
	if k.Paika.CPU > 0 {
		const period = 100000
		quota := int64(k.Paika.CPU * period)
		if quota < 1000 {
			quota = 1000
		}
		if err := writeFile(filepath.Join(dir, "cpu.max"), fmt.Sprintf("%d %d", quota, period)); err != nil {
			return fail(fmt.Errorf("не записать cpu.max: %w", err))
		}
	}
	if k.Paika.Mem > 0 {
		if err := writeFile(filepath.Join(dir, "memory.max"), strconv.FormatInt(k.Paika.Mem, 10)); err != nil {
			return fail(fmt.Errorf("не записать memory.max: %w", err))
		}
		// Без этого процесс уйдёт в своп и лимит будет мягким.
		_ = writeFile(filepath.Join(dir, "memory.swap.max"), "0")
	}
	return e, nil
}

func writeFile(path, val string) error {
	return os.WriteFile(path, []byte(val), 0o644)
}

func (e *cgroupEngine) pgid() int { return e.pid }

func (e *cgroupEngine) prepare(cmd *exec.Cmd) error {
	f, err := os.Open(e.dir)
	if err != nil {
		return err
	}
	e.fd = f
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(f.Fd())
	return nil
}

func (e *cgroupEngine) started(p *os.Process) error {
	e.pid = p.Pid
	if e.fd != nil {
		_ = e.fd.Close()
		e.fd = nil
	}
	return nil
}

func (e *cgroupEngine) kill(graceful bool) {
	killGroup(e.pid, graceful)
	cgroupKill(e.dir)
}

func (e *cgroupEngine) finish(ps *os.ProcessState) (*Usage, string) {
	if e.fd != nil {
		_ = e.fd.Close()
		e.fd = nil
	}
	if ps == nil {
		_ = os.Remove(e.dir)
		return nil, ""
	}
	cgroupKill(e.dir)
	u := rusage(ps)
	if u == nil {
		u = &Usage{}
	}
	if user, sys, ok := cgroupCPU(e.dir); ok {
		u.UserMs, u.SysMs = user.Milliseconds(), sys.Milliseconds()
	}
	if peak := readInt(filepath.Join(e.dir, "memory.peak")); peak > 0 {
		u.PeakMem = peak
	}
	reason := ""
	if events, err := os.ReadFile(filepath.Join(e.dir, "memory.events")); err == nil {
		for _, line := range strings.Split(string(events), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "oom_kill" && f[1] != "0" {
				reason = ReasonMem
			}
		}
	}
	for i := 0; i < 40; i++ {
		if err := os.Remove(e.dir); err == nil || errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return u, reason
}

func cgroupKill(dir string) {
	if dir == "" {
		return
	}
	if writeFile(filepath.Join(dir, "cgroup.kill"), "1") == nil {
		return
	}
	// Ядро старше 5.14: убиваем по списку.
	for _, pid := range cgroupProcs(dir) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func cgroupProcs(dir string) []int {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func cgroupCPU(dir string) (user, sys time.Duration, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "user_usec":
			user, ok = time.Duration(v)*time.Microsecond, true
		case "system_usec":
			sys = time.Duration(v) * time.Microsecond
		}
	}
	return user, sys, ok
}

func readInt(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return -1
	}
	return v
}

// killEngineRest добивает процессы в cgroup камеры, если она есть.
func killEngineRest(k *Kamera) {
	if k.Engine == EngineCgroup && strings.HasPrefix(k.EngineInfo, cgroupRoot) {
		cgroupKill(k.EngineInfo)
	}
}

// watchLimits на Linux не нужен: лимиты держат cgroup или rlimit,
// а на жёстком RLIMIT_CPU ядро шлёт SIGKILL.
func watchLimits(*Kamera, func(string)) func() { return func() {} }

// procStat — нужные поля из /proc/<pid>/stat.
type procStat struct {
	state     byte
	pgrp      int
	utime     int64
	stime     int64
	cutime    int64
	cstime    int64
	starttime uint64
	comm      string
}

func readStat(pid int) (*procStat, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, err
	}
	// Имя процесса в скобках может содержать пробелы и скобки.
	l := bytes.IndexByte(data, '(')
	r := bytes.LastIndexByte(data, ')')
	if l < 0 || r < l {
		return nil, errors.New("непонятный /proc/stat")
	}
	f := strings.Fields(string(data[r+1:]))
	if len(f) < 20 {
		return nil, errors.New("короткий /proc/stat")
	}
	st := &procStat{comm: string(data[l+1 : r])}
	if len(f[0]) > 0 {
		st.state = f[0][0]
	}
	st.pgrp, _ = strconv.Atoi(f[2])
	st.utime, _ = strconv.ParseInt(f[11], 10, 64)
	st.stime, _ = strconv.ParseInt(f[12], 10, 64)
	st.cutime, _ = strconv.ParseInt(f[13], 10, 64)
	st.cstime, _ = strconv.ParseInt(f[14], 10, 64)
	st.starttime, _ = strconv.ParseUint(f[19], 10, 64)
	return st, nil
}

func procStart(pid int) uint64 {
	st, err := readStat(pid)
	if err != nil {
		return 0
	}
	return st.starttime
}

func isZombie(pid int) bool {
	st, err := readStat(pid)
	return err == nil && (st.state == 'Z' || st.state == 'X')
}

func isStopped(pid int) bool {
	st, err := readStat(pid)
	return err == nil && st.state == 'T'
}

func ticks(n int64) time.Duration {
	return time.Duration(n) * time.Second / time.Duration(clockTicks)
}

func cmdline(pid int, comm string) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(data) == 0 {
		return "[" + comm + "]"
	}
	return strings.TrimSpace(strings.ReplaceAll(string(bytes.TrimRight(data, "\x00")), "\x00", " "))
}

func statusKB(pid int, key string) int64 {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return -1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), key+":"); ok {
			fs := strings.Fields(rest)
			if len(fs) > 0 {
				v, _ := strconv.ParseInt(fs[0], 10, 64)
				return v * 1024
			}
		}
	}
	return -1
}

// groupPids ищет в /proc все процессы группы pgid.
func groupPids(pgid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if st, err := readStat(pid); err == nil && st.pgrp == pgid && st.state != 'Z' {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// liveStats собирает данные о камере из cgroup и /proc.
func liveStats(k *Kamera) (*Stats, error) {
	if !Alive(k.PID, k.ProcStart) && !groupAlive(k.PGID) {
		return nil, errors.New("процессов камеры уже нет")
	}
	s := &Stats{Mem: -1, PeakMem: -1, Source: "/proc"}
	var pids []int
	if k.Engine == EngineCgroup && k.EngineInfo != "" {
		if _, err := os.Stat(k.EngineInfo); err == nil {
			s.Source = "cgroup и /proc"
			pids = cgroupProcs(k.EngineInfo)
			s.Mem = readInt(filepath.Join(k.EngineInfo, "memory.current"))
			s.PeakMem = readInt(filepath.Join(k.EngineInfo, "memory.peak"))
			if user, sys, ok := cgroupCPU(k.EngineInfo); ok {
				s.CPUUser, s.CPUSys = user, sys
			}
		}
	}
	if pids == nil {
		pids = groupPids(k.PGID)
	}
	var memSum int64
	cgCPU := s.CPUUser+s.CPUSys > 0
	for _, pid := range pids {
		st, err := readStat(pid)
		if err != nil {
			continue
		}
		rss := statusKB(pid, "VmRSS")
		if rss > 0 {
			memSum += rss
		}
		if !cgCPU {
			s.CPUUser += ticks(st.utime)
			s.CPUSys += ticks(st.stime)
			if pid == k.PID {
				s.CPUUser += ticks(st.cutime)
				s.CPUSys += ticks(st.cstime)
			}
		}
		s.Procs = append(s.Procs, Proc{PID: pid, Cmd: cmdline(pid, st.comm), Mem: rss})
		fds, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/fd")
		if err != nil {
			s.FilesNote = "нет доступа к /proc/<pid>/fd"
			continue
		}
		for _, fd := range fds {
			s.FilesCount++
			if len(s.Files) < maxFiles {
				target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/" + fd.Name())
				if err != nil {
					continue
				}
				s.Files = append(s.Files, fmt.Sprintf("%d:%s → %s", pid, fd.Name(), target))
			}
		}
	}
	if s.Mem < 0 {
		s.Mem = memSum
	}
	s.CPUTotal = s.CPUUser + s.CPUSys
	return s, nil
}

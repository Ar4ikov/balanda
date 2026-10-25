//go:build unix && !linux

package kamera

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxFiles = 50

// На macOS RLIMIT_AS не соблюдается, в OpenBSD его нет: там память
// держит сторож.
var useRlimitAS = runtime.GOOS != "darwin" && runtime.GOOS != "openbsd"

// ru_maxrss: на macOS в байтах, на BSD в килобайтах.
var maxrssUnit = func() int64 {
	if runtime.GOOS == "darwin" {
		return 1
	}
	return 1024
}()

func newEngines(k *Kamera) ([]engineMaker, error) {
	if k.Paika.Empty() {
		return []engineMaker{func() (engine, string, string, error) {
			return &groupEngine{}, EngineNone, "", nil
		}}, nil
	}
	return []engineMaker{func() (engine, string, string, error) {
		k.Warnings = append(k.Warnings, rlimitWarnings(k.Paika)...)
		return &groupEngine{limits: true, p: k.Paika}, EngineRlimit, "", nil
	}}, nil
}

// EngineFor сообщает, какой движок достанется новой камере с лимитами.
func EngineFor() (string, string) {
	return EngineRlimit, "cgroups есть только в Linux"
}

func killEngineRest(*Kamera) {}

// watchLimits раз в полсекунды суммирует RSS и процессорное время
// группы и гасит камеру, если сумма вышла за пайку. Память сторож
// держит там, где RLIMIT_AS не работает (macOS, OpenBSD). Процессорное
// время — везде: macOS не добивает SIGKILL процесс, который игнорирует
// SIGXCPU (так делают, например, программы на Go).
func watchLimits(k *Kamera, onExceed func(reason string)) func() {
	memLimit := k.Paika.Mem
	if useRlimitAS {
		memLimit = 0
	}
	cpuLimit := time.Duration(k.Paika.CPUSeconds()) * time.Second
	if memLimit <= 0 && cpuLimit <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				procs, err := psGroup(k.PGID)
				if err != nil {
					continue
				}
				var rss int64
				var cpu time.Duration
				for _, p := range procs {
					rss += p.rss
					cpu += p.cpu
				}
				switch {
				case memLimit > 0 && rss > memLimit:
					onExceed(ReasonMem)
					return
				case cpuLimit > 0 && cpu > cpuLimit:
					onExceed(ReasonCPU)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

type psProc struct {
	pid, pgid int
	rss       int64
	cpu       time.Duration
	stat      string
	cmd       string
}

// psAll читает таблицу процессов через ps: это есть и в macOS, и в BSD.
func psAll() ([]psProc, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,pgid=,rss=,time=,stat=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var res []psProc
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		pgid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		res = append(res, psProc{
			pid: pid, pgid: pgid, rss: rss * 1024,
			cpu: parsePsTime(f[3]), stat: f[4],
			cmd: strings.Join(f[5:], " "),
		})
	}
	return res, nil
}

func psGroup(pgid int) ([]psProc, error) {
	all, err := psAll()
	if err != nil {
		return nil, err
	}
	var res []psProc
	for _, p := range all {
		if p.pgid == pgid && !strings.HasPrefix(p.stat, "Z") {
			res = append(res, p)
		}
	}
	return res, nil
}

// parsePsTime разбирает время ps: [[dd-]hh:]mm:ss[.cc].
func parsePsTime(s string) time.Duration {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		days, _ = strconv.ParseInt(d, 10, 64)
		s = rest
	}
	parts := strings.Split(s, ":")
	var total float64
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		total = total*60 + v
	}
	return time.Duration(days)*24*time.Hour + time.Duration(total*float64(time.Second))
}

// liveStats собирает данные через ps и lsof.
func liveStats(k *Kamera) (*Stats, error) {
	if !Alive(k.PID, k.ProcStart) && !groupAlive(k.PGID) {
		return nil, errors.New("процессов камеры уже нет")
	}
	procs, err := psGroup(k.PGID)
	if err != nil {
		return nil, err
	}
	s := &Stats{Mem: 0, PeakMem: -1, Source: "ps"}
	for _, p := range procs {
		s.Mem += p.rss
		s.CPUTotal += p.cpu
		s.Procs = append(s.Procs, Proc{PID: p.pid, Cmd: p.cmd, Mem: p.rss})
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		s.FilesNote = "нет lsof"
		return s, nil
	}
	s.Source = "ps и lsof"
	out, err := exec.Command("lsof", "-n", "-P", "-g", strconv.Itoa(k.PGID), "-F", "pfn").Output()
	if err != nil && len(out) == 0 {
		s.FilesNote = "lsof ничего не вернул"
		return s, nil
	}
	var pid, fd string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'f':
			fd = line[1:]
		case 'n':
			s.FilesCount++
			if len(s.Files) < maxFiles {
				s.Files = append(s.Files, fmt.Sprintf("%s:%s → %s", pid, fd, line[1:]))
			}
		}
	}
	return s, nil
}

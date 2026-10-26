//go:build windows

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	jobObjectQuery     = 0x0004
	jobObjectTerminate = 0x0008

	jobCPURateEnable  = 0x1
	jobCPURateHardCap = 0x4

	createNoWindow = 0x08000000
	stillActive    = 259
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procOpenJobObjectW          = kernel32.NewProc("OpenJobObjectW")
	procK32GetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	procGetProcessHandleCount   = kernel32.NewProc("GetProcessHandleCount")
)

// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION
type cpuRateInfo struct {
	ControlFlags uint32
	CPURate      uint32
}

// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
type basicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// JOBOBJECT_BASIC_PROCESS_ID_LIST с запасом на 512 процессов.
type pidList struct {
	Assigned uint32
	InList   uint32
	IDs      [512]uintptr
}

// PROCESS_MEMORY_COUNTERS
type memCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func newEngines(k *Kamera) ([]engineMaker, error) {
	return []engineMaker{func() (engine, string, string, error) {
		e, err := newJobEngine(k)
		if err != nil {
			return nil, "", "", err
		}
		return e, EngineJob, e.name, nil
	}}, nil
}

// EngineFor сообщает, какой движок достанется новой камере с лимитами.
func EngineFor() (string, string) {
	return EngineJob, "Windows Job Objects"
}

type jobEngine struct {
	name string
	job  windows.Handle
	proc windows.Handle
	p    Paika
	iz   Izolyaciya
	dir  string
	sid  *windows.SID
	tmp  string
}

func newJobEngine(k *Kamera) (*jobEngine, error) {
	name := fmt.Sprintf(`Local\balanda-kamera-%d-%d`, k.ID, os.Getpid())
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateJobObject(nil, n)
	if err != nil {
		return nil, fmt.Errorf("не создать Job Object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if k.Paika.Mem > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(k.Paika.Mem)
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("не настроить Job Object: %w", err)
	}
	if k.Paika.CPU > 0 {
		rate := k.Paika.CPU / float64(runtime.NumCPU()) * 10000
		switch {
		case rate >= 10000:
			k.Warnings = append(k.Warnings, fmt.Sprintf("cpu=%g не меньше числа ядер (%d), ограничивать нечего", k.Paika.CPU, runtime.NumCPU()))
		default:
			if rate < 1 {
				rate = 1
			}
			ci := cpuRateInfo{ControlFlags: jobCPURateEnable | jobCPURateHardCap, CPURate: uint32(rate)}
			if _, err := windows.SetInformationJobObject(h, windows.JobObjectCpuRateControlInformation,
				uintptr(unsafe.Pointer(&ci)), uint32(unsafe.Sizeof(ci))); err != nil {
				k.Warnings = append(k.Warnings, "cpu не ограничен: Windows отказал в CPU rate control: "+err.Error())
			}
		}
	}
	e := &jobEngine{name: name, job: h, p: k.Paika, iz: k.Izolyaciya, dir: k.Dir}
	if e.iz.On() {
		sid, err := appContainerSID(containerName(k.Dir))
		if err != nil {
			_ = windows.CloseHandle(h)
			return nil, errors.New(isolationHint(err))
		}
		tmp, err := containerTemp(sid)
		if err != nil {
			_ = windows.CloseHandle(h)
			return nil, errors.New(isolationHint(err))
		}
		e.sid, e.tmp = sid, tmp
	}
	return e, nil
}

func (e *jobEngine) prepare(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Процесс стартует приостановленным и попадает в Job Object
	// до того, как успеет запустить кого-то ещё.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return nil
}

// start: при изоляции процесс создаётся в AppContainer, иначе — exec.Cmd.
func (e *jobEngine) start(cmd *exec.Cmd) (*os.Process, bool, error) {
	if !e.iz.On() {
		return nil, false, nil
	}
	p, err := startInAppContainer(cmd, e.sid, e.iz, e.dir, e.tmp)
	return p, true, err
}

func (e *jobEngine) started(p *os.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(p.Pid))
	if err != nil {
		_ = p.Kill()
		return fmt.Errorf("не открыть процесс: %w", err)
	}
	e.proc = h
	if err := windows.AssignProcessToJobObject(e.job, h); err != nil {
		_ = windows.TerminateProcess(h, 1)
		return fmt.Errorf("не добавить процесс в Job Object: %w", err)
	}
	if err := resumeProcess(uint32(p.Pid)); err != nil {
		_ = windows.TerminateJobObject(e.job, 1)
		return fmt.Errorf("не разбудить процесс: %w", err)
	}
	return nil
}

func resumeProcess(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer closeHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			continue
		}
		if _, err := windows.ResumeThread(th); err == nil {
			resumed++
		}
		_ = windows.CloseHandle(th)
	}
	if resumed == 0 {
		return errors.New("не найден ни один поток")
	}
	return nil
}

func (e *jobEngine) kill(bool) {
	if e.job != 0 {
		_ = windows.TerminateJobObject(e.job, 1)
	}
}

func (e *jobEngine) finish(ps *os.ProcessState) (*Usage, string) {
	defer func() {
		if e.proc != 0 {
			_ = windows.CloseHandle(e.proc)
			e.proc = 0
		}
		if e.job != 0 {
			_ = windows.CloseHandle(e.job)
			e.job = 0
		}
	}()
	if ps == nil || e.job == 0 {
		return nil, ""
	}
	// Процесс вышел, остальных в Job Object добиваем.
	_ = windows.TerminateJobObject(e.job, 1)
	u := &Usage{PeakMem: -1}
	var acc basicAccounting
	if windows.QueryInformationJobObject(e.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil) == nil {
		u.UserMs = acc.TotalUserTime / 10000
		u.SysMs = acc.TotalKernelTime / 10000
	}
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if windows.QueryInformationJobObject(e.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil) == nil {
		u.PeakMem = int64(ext.PeakJobMemoryUsed)
	}
	reason := ""
	if e.p.Mem > 0 && u.PeakMem >= e.p.Mem-e.p.Mem/50 && ps.ExitCode() != 0 {
		reason = ReasonMem
	}
	return u, reason
}

func closeHandle(h windows.Handle) { _ = windows.CloseHandle(h) }

func openJob(name string, access uint32) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	r, _, err := procOpenJobObjectW.Call(uintptr(access), 0, uintptr(unsafe.Pointer(n)))
	if r == 0 {
		return 0, err
	}
	return windows.Handle(r), nil
}

func setGroup(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
}

func restoreTerminal() {}

// forwardSignals: Ctrl+C консоль сама доставит процессу, надзиратель
// его только переживает.
func forwardSignals(engine, bool) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ch:
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | createNoWindow,
		HideWindow:    true,
	}
}

func exitInfo(ps *os.ProcessState, waitErr error) (int, string) {
	if ps == nil {
		if waitErr != nil {
			return 1, ""
		}
		return 0, ""
	}
	return ps.ExitCode(), ""
}

func runZek([]string) int {
	fmt.Fprintln(os.Stderr, "balanda: __zek на Windows не нужен")
	return ExitNoStart
}

func watchLimits(*Kamera, func(string)) func() { return func() {} }

// Alive проверяет, жив ли процесс, и при нужде сверяет время старта.
func Alive(pid int, start uint64) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer closeHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	if start != 0 {
		if cur := startOf(h); cur != 0 && cur != start {
			return false
		}
	}
	return true
}

func startOf(h windows.Handle) uint64 {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	return uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
}

func procStart(pid int) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer closeHandle(h)
	return startOf(h)
}

// killKamera гасит камеру из другого процесса (otboy).
func killKamera(k *Kamera, _ bool) error {
	if k.EngineInfo != "" {
		if job, err := openJob(k.EngineInfo, jobObjectTerminate); err == nil {
			defer closeHandle(job)
			return windows.TerminateJobObject(job, 1)
		}
	}
	if !Alive(k.PID, k.ProcStart) {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(k.PID))
	if err != nil {
		return err
	}
	defer closeHandle(h)
	return windows.TerminateProcess(h, 1)
}

func processNames() map[uint32]string {
	names := map[uint32]string{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return names
	}
	defer closeHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		names[pe.ProcessID] = windows.UTF16ToString(pe.ExeFile[:])
	}
	return names
}

func procMemAndHandles(pid uint32) (int64, int) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return -1, 0
	}
	defer closeHandle(h)
	var mc memCounters
	mc.CB = uint32(unsafe.Sizeof(mc))
	mem := int64(-1)
	if r, _, _ := procK32GetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.CB)); r != 0 {
		mem = int64(mc.WorkingSetSize)
	}
	var handles uint32
	if r, _, _ := procGetProcessHandleCount.Call(uintptr(h), uintptr(unsafe.Pointer(&handles))); r == 0 {
		handles = 0
	}
	return mem, int(handles)
}

// liveStats читает данные Job Object камеры.
func liveStats(k *Kamera) (*Stats, error) {
	if !Alive(k.PID, k.ProcStart) {
		return nil, errors.New("процессов камеры уже нет")
	}
	s := &Stats{Mem: 0, PeakMem: -1, Source: "Job Object"}
	pids := []uint32{uint32(k.PID)}
	if job, err := openJob(k.EngineInfo, jobObjectQuery); err == nil {
		defer closeHandle(job)
		var acc basicAccounting
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&acc)), uint32(unsafe.Sizeof(acc)), nil) == nil {
			s.CPUUser = time.Duration(acc.TotalUserTime) * 100
			s.CPUSys = time.Duration(acc.TotalKernelTime) * 100
		}
		var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil) == nil {
			s.PeakMem = int64(ext.PeakJobMemoryUsed)
		}
		var list pidList
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil) == nil && list.InList > 0 {
			pids = pids[:0]
			for i := uint32(0); i < list.InList && int(i) < len(list.IDs); i++ {
				pids = append(pids, uint32(list.IDs[i]))
			}
		}
	} else {
		s.Source = "процесс (Job Object не открылся)"
	}
	names := processNames()
	handles := 0
	for _, pid := range pids {
		mem, h := procMemAndHandles(pid)
		if mem > 0 {
			s.Mem += mem
		}
		handles += h
		s.Procs = append(s.Procs, Proc{PID: int(pid), Cmd: names[pid], Mem: mem})
	}
	s.CPUTotal = s.CPUUser + s.CPUSys
	s.FilesNote = fmt.Sprintf("Windows не отдаёт список открытых файлов без отладочных прав; открытых дескрипторов (handles): %d", handles)
	return s, nil
}

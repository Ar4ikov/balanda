//go:build linux

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// isolateCmd запускает процесс в новых пространствах имён: пользователей,
// монтирования, PID, IPC, имени хоста и, если сеть закрыта, сети. Root не
// нужен: снаружи процесс остаётся тем же пользователем, а права на
// монтирование есть только внутри своего пространства имён.
func isolateCmd(cmd *exec.Cmd, iz Izolyaciya) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	a := cmd.SysProcAttr
	flags := unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWIPC | unix.CLONE_NEWUTS
	if !iz.Set {
		flags |= unix.CLONE_NEWNET
	}
	if os.Geteuid() == 0 {
		// root может строить стены и без пространства имён пользователя,
		// а в нём потерял бы доступ к чужим файлам. Права у команды
		// __zek отнимет перед запуском.
		a.Cloneflags |= uintptr(flags)
		return nil
	}
	a.Cloneflags |= uintptr(flags | unix.CLONE_NEWUSER)
	uid, gid := os.Getuid(), os.Getgid()
	a.UidMappings = []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}}
	a.GidMappings = []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}}
	a.GidMappingsEnableSetgroups = false
	// Права нужны служебному __zek, чтобы собрать стены. Команде они не
	// достаются: перед её запуском __zek их сбрасывает.
	a.AmbientCaps = []uintptr{unix.CAP_SYS_ADMIN, unix.CAP_NET_ADMIN}
	return nil
}

// isolationHint объясняет, почему одиночка может не собраться.
func isolationHint(err error) string {
	var b strings.Builder
	b.WriteString("изоляция недоступна: " + err.Error())
	if data, e := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); e == nil && strings.TrimSpace(string(data)) == "1" {
		exe, _ := os.Executable()
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		b.WriteString("\nUbuntu 23.10+ через AppArmor запрещает программам свои пространства имён. Варианты:" +
			"\n  - поставить balanda пакетом .deb: он кладёт профиль AppArmor с разрешением;" +
			"\n  - разрешить только этому бинарнику:" +
			"\n      printf 'abi <abi/4.0>,\\ninclude <tunables/global>\\nprofile balanda " + exe + " flags=(unconfined) {\\n  userns,\\n}\\n' | sudo tee /etc/apparmor.d/balanda >/dev/null" +
			"\n      sudo apparmor_parser -r /etc/apparmor.d/balanda" +
			"\n  - снять ограничение для всех: sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0;")
	} else if data, e := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); e == nil && strings.TrimSpace(string(data)) == "0" {
		b.WriteString("\nЯдро запрещает пространства имён без root. Варианты:" +
			"\n  - разрешить: sudo sysctl -w kernel.unprivileged_userns_clone=1;")
	} else {
		b.WriteString("\nНужно ядро Linux 5.12+ с пространствами имён пользователей (в контейнерах их часто режет seccomp). Варианты:")
	}
	b.WriteString("\n  - запустить без изоляции: balanda sadit --bez-izolyacii ...")
	return b.String()
}

// zekRun — служебный режим внутри камеры.
func zekRun(o zekOpts) int {
	if o.odinochka {
		if err := odinochkaSetup(o); err != nil {
			fmt.Fprintln(os.Stderr, "balanda: "+isolationHint(err))
			return ExitNoStart
		}
		if o.proverka {
			return 0
		}
	}
	if err := applyRlimits(o); err != nil {
		fmt.Fprintln(os.Stderr, "balanda:", err)
		return ExitNoStart
	}
	if o.odinochka {
		return initRun(o)
	}
	err := syscall.Exec(o.path, o.argv, os.Environ())
	fmt.Fprintf(os.Stderr, "balanda: не запустить %s: %v\n", o.path, err)
	return ExitNoStart
}

// odinochkaSetup строит стены внутри новых пространств имён.
func odinochkaSetup(o zekOpts) error {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("отделить точки монтирования: %w", err)
	}
	// Вся система — только на чтение.
	ro := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}
	if err := unix.MountSetattr(unix.AT_FDCWD, "/", unix.AT_RECURSIVE, &ro); err != nil {
		return fmt.Errorf("сделать систему только для чтения: %w", err)
	}
	// Каталоги для записи снимаем копией до того, как их закроют
	// /tmp, маски секретов или спрятанный дом, и возвращаем в конце.
	type tree struct {
		path string
		fd   int
	}
	var trees []tree
	defer func() {
		for _, t := range trees {
			_ = unix.Close(t.fd)
		}
	}()
	seen := map[string]bool{}
	for _, p := range append([]string{o.dir}, o.zapis...) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		fd, err := unix.OpenTree(unix.AT_FDCWD, p, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_RECURSIVE)
		if err != nil {
			return fmt.Errorf("открыть %s на запись: %w", p, err)
		}
		trees = append(trees, tree{p, fd})
		rw := unix.MountAttr{Attr_clr: unix.MOUNT_ATTR_RDONLY}
		if err := unix.MountSetattr(fd, "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE, &rw); err != nil {
			return fmt.Errorf("открыть %s на запись: %w", p, err)
		}
	}
	// Каталог самой команды: если он под /tmp или в спрятанном доме,
	// после масок его не станет видно. Снимаем копию только для чтения.
	exeDir := filepath.Dir(o.path)
	exeFd := -1
	if fd, err := unix.OpenTree(unix.AT_FDCWD, exeDir, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC); err == nil {
		exeFd = fd
		defer unix.Close(fd)
	}
	// Свои пустые /tmp, /var/tmp и /dev/shm.
	for _, t := range []string{"/tmp", "/var/tmp", "/dev/shm"} {
		if !isDir(t) {
			continue
		}
		if err := unix.Mount("tmpfs", t, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777"); err != nil {
			return fmt.Errorf("свой %s: %w", t, err)
		}
	}
	home := userHome()
	// Сокеты сессии пользователя (dbus, агенты) и чужие runtime-каталоги.
	if isDir("/run/user") {
		if err := maskDir("/run/user"); err != nil {
			return err
		}
	}
	if o.bezDoma && home != "" && isDir(home) {
		if err := maskDir(home); err != nil {
			return err
		}
	} else {
		for _, p := range secretPaths(home) {
			if err := maskPath(p); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(o.path); err != nil && exeFd >= 0 {
		if err := os.MkdirAll(exeDir, 0o755); err != nil {
			return fmt.Errorf("вернуть каталог команды %s: %w", exeDir, err)
		}
		if err := unix.MoveMount(exeFd, "", unix.AT_FDCWD, exeDir, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
			return fmt.Errorf("вернуть каталог команды %s: %w", exeDir, err)
		}
	}
	for _, t := range trees {
		if err := os.MkdirAll(t.path, 0o755); err != nil {
			return fmt.Errorf("вернуть %s: %w", t.path, err)
		}
		if err := unix.MoveMount(t.fd, "", unix.AT_FDCWD, t.path, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
			return fmt.Errorf("вернуть %s на запись: %w", t.path, err)
		}
	}
	// В новом пространстве PID процессы хоста не видны, если /proc свой.
	// В контейнерах ядро может не дать смонтировать proc; тогда остаётся
	// старый, только на чтение.
	_ = unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "")
	_ = unix.Sethostname([]byte("kamera"))
	if !o.set {
		if err := loUp(); err != nil {
			return fmt.Errorf("поднять lo: %w", err)
		}
	}
	if err := os.Chdir(o.dir); err != nil {
		return err
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// maskDir закрывает каталог пустым tmpfs, доступным только владельцу.
func maskDir(p string) error {
	if err := unix.Mount("tmpfs", p, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "mode=0700,size=1m"); err != nil {
		return fmt.Errorf("спрятать %s: %w", p, err)
	}
	return nil
}

// maskPath прячет файл, сокет или каталог, если он есть.
func maskPath(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil
		}
		if fi, err = os.Stat(real); err != nil {
			return nil
		}
		p = real
	}
	if fi.IsDir() {
		return maskDir(p)
	}
	if err := unix.Mount("/dev/null", p, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("спрятать %s: %w", p, err)
	}
	return nil
}

// loUp поднимает петлю lo в новом сетевом пространстве имён.
func loUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// initRun — __zek остаётся процессом 1 в новом пространстве PID: запускает
// команду, собирает осиротевших потомков и выходит с кодом команды.
// Когда он выходит, ядро гасит всех, кто остался в камере.
func initRun(o zekOpts) int {
	// Поток, который вызовет fork, передаст ребёнку свои права.
	// Сбрасываем их здесь: команда не получит ни CAP_SYS_ADMIN, ни
	// возможности поднять права через setuid-файлы.
	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "balanda: не сбросить права:", err)
		return ExitNoStart
	}
	if os.Geteuid() == 0 {
		// Под root пространства имён пользователя нет: команда осталась
		// бы настоящим root. Убираем все capabilities из ограничивающего
		// набора, и после exec у неё не будет ни одной.
		if err := dropBoundingSet(); err != nil {
			fmt.Fprintln(os.Stderr, "balanda: не сбросить права root:", err)
			return ExitNoStart
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "balanda: не запретить подъём прав:", err)
		return ExitNoStart
	}
	cmd := &exec.Cmd{
		Path:   o.path,
		Args:   o.argv,
		Env:    cleanEnv(os.Environ()),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGUSR1, syscall.SIGUSR2)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "balanda: не запустить %s: %v\n", o.path, err)
		return ExitNoStart
	}
	runtime.UnlockOSThread()
	// Команда не должна читать память и /proc служебного процесса.
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
	pid := cmd.Process.Pid
	go func() {
		for s := range sigs {
			// SIGINT, SIGQUIT и SIGHUP от терминала уже пришли всей группе.
			switch s {
			case syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2:
				_ = syscall.Kill(pid, s.(syscall.Signal))
			}
		}
	}()
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return 1
		}
		if wpid != pid {
			continue
		}
		switch {
		case ws.Exited():
			return ws.ExitStatus()
		case ws.Signaled():
			return 128 + int(ws.Signal())
		}
	}
}

// dropBoundingSet убирает все capabilities из ограничивающего набора
// текущего потока.
func dropBoundingSet() error {
	for c := 0; c <= 63; c++ {
		err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(c), 0, 0, 0)
		if errors.Is(err, unix.EINVAL) {
			break // capabilities с таким номером ядро не знает
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ProbeIzolyaciya проверяет, соберётся ли одиночка на этой машине.
func ProbeIzolyaciya() error {
	self, err := selfExe()
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = os.TempDir()
	}
	o := zekOpts{odinochka: true, proverka: true, dir: dir, path: "/bin/true", argv: []string{"true"}}
	cmd := exec.Command(self, append([]string{argZek}, o.args()...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := isolateCmd(cmd, Izolyaciya{}); err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return errors.New(isolationHint(err))
		}
		return errors.New(strings.TrimPrefix(msg, "balanda: "))
	}
	return nil
}

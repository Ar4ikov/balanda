//go:build windows

package kamera

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	userenv                                       = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile                 = userenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procGetAppContainerFolderPath                 = userenv.NewProc("GetAppContainerFolderPath")
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	hresultAlreadyExists                    = 0x800700B7
	aceTypeAccessAllowed                    = 0

	fileAllAccess   = 0x001F01FF
	fileReadExecute = 0x001200A9
)

// SECURITY_CAPABILITIES
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// containerName — имя профиля AppContainer. Своё для каждого рабочего
// каталога: камера из одного проекта не получит доступ к другому.
func containerName(dir string) string {
	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
	return "balanda.kamera." + hex.EncodeToString(h[:8])
}

func utf16Ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// appContainerSID создаёт профиль AppContainer или берёт существующий.
func appContainerSID(name string) (*windows.SID, error) {
	var sid *windows.SID
	r, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(utf16Ptr(name))),
		uintptr(unsafe.Pointer(utf16Ptr("balanda"))),
		uintptr(unsafe.Pointer(utf16Ptr("камера balanda"))),
		0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(r) == hresultAlreadyExists {
		r, _, _ = procDeriveAppContainerSidFromAppContainerName.Call(
			uintptr(unsafe.Pointer(utf16Ptr(name))), uintptr(unsafe.Pointer(&sid)))
	}
	if r != 0 {
		return nil, fmt.Errorf("не создать AppContainer: HRESULT 0x%08X", uint32(r))
	}
	cp, err := sid.Copy()
	_ = windows.FreeSid(sid)
	return cp, err
}

// containerTemp — каталог для временных файлов камеры внутри папки
// профиля AppContainer: доступ к ней Windows выдаёт сама.
func containerTemp(sid *windows.SID) (string, error) {
	var p *uint16
	r, _, _ := procGetAppContainerFolderPath.Call(uintptr(unsafe.Pointer(utf16Ptr(sid.String()))), uintptr(unsafe.Pointer(&p)))
	if r != 0 {
		return "", fmt.Errorf("нет папки AppContainer: HRESULT 0x%08X", uint32(r))
	}
	dir := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	tmp := filepath.Join(dir, "Temp")
	return tmp, os.MkdirAll(tmp, 0o700)
}

func aceCount(acl *windows.ACL) uint16 {
	// ACL: AclRevision, Sbz1 (по байту), AclSize, AceCount (uint16).
	return *(*uint16)(unsafe.Add(unsafe.Pointer(acl), 4))
}

// hasAce ищет разрешающую запись для sid с нужными правами.
func hasAce(acl *windows.ACL, sid *windows.SID, mask uint32) bool {
	if acl == nil {
		return false
	}
	for i := uint32(0); i < uint32(aceCount(acl)); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			continue
		}
		if ace.Header.AceType != aceTypeAccessAllowed || uint32(ace.Mask)&mask != mask {
			continue
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			return true
		}
	}
	return false
}

// grantAccess добавляет в ACL каталога разрешение для AppContainer.
// Запись наследуется вложенными файлами. Если она уже есть, ACL не
// трогается: перезапись обходит всё дерево и на больших каталогах долгая.
func grantAccess(path string, sid *windows.SID, mask uint32) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if hasAce(dacl, sid, mask) {
		return nil
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, dacl)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// networkCaps — возможности AppContainer для доступа в сеть.
func networkCaps() ([]windows.SIDAndAttributes, error) {
	var caps []windows.SIDAndAttributes
	for _, t := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinCapabilityInternetClientSid,
		windows.WinCapabilityInternetClientServerSid,
		windows.WinCapabilityPrivateNetworkClientServerSid,
	} {
		sid, err := windows.CreateWellKnownSid(t)
		if err != nil {
			return nil, err
		}
		caps = append(caps, windows.SIDAndAttributes{Sid: sid, Attributes: windows.SE_GROUP_ENABLED})
	}
	return caps, nil
}

// envBlock собирает блок окружения для CreateProcess.
func envBlock(env []string) *uint16 {
	var b []uint16
	for _, kv := range env {
		if strings.ContainsRune(kv, 0) {
			continue
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	if len(b) == 1 {
		b = append(b, 0)
	}
	return &b[0]
}

func setEnv(env []string, key, val string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+val)
}

// stdHandle возвращает наследуемую копию дескриптора для потока ввода-вывода.
func stdHandle(v any, write bool) (windows.Handle, error) {
	var h windows.Handle
	switch f := v.(type) {
	case *os.File:
		if f == nil {
			return nullHandle(write)
		}
		h = windows.Handle(f.Fd())
	case nil:
		return nullHandle(write)
	default:
		if v == io.Discard {
			return nullHandle(write)
		}
		return 0, errors.New("в изолированной камере ввод и вывод должны быть файлами")
	}
	self := windows.CurrentProcess()
	var dup windows.Handle
	err := windows.DuplicateHandle(self, h, self, &dup, 0, true, windows.DUPLICATE_SAME_ACCESS)
	return dup, err
}

func nullHandle(write bool) (windows.Handle, error) {
	access := uint32(windows.GENERIC_READ)
	if write {
		access = windows.GENERIC_WRITE
	}
	sa := windows.SecurityAttributes{InheritHandle: 1}
	sa.Length = uint32(unsafe.Sizeof(sa))
	return windows.CreateFile(utf16Ptr("NUL"), access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, windows.OPEN_EXISTING, 0, 0)
}

// startInAppContainer создаёт процесс в AppContainer приостановленным.
func startInAppContainer(cmd *exec.Cmd, sid *windows.SID, iz Izolyaciya, dir, tmp string) (*os.Process, error) {
	// Доступ к рабочему каталогу и --zapis — на запись, к каталогу
	// программы — на чтение (если программа лежит, например, в профиле
	// пользователя, куда AppContainer иначе не пускают).
	for _, p := range iz.Writable(dir) {
		if err := grantAccess(p, sid, fileAllAccess); err != nil {
			return nil, fmt.Errorf("не выдать камере доступ к %s: %w", p, err)
		}
	}
	_ = grantAccess(filepath.Dir(cmd.Path), sid, fileReadExecute)

	sc := securityCapabilities{AppContainerSid: sid}
	if iz.Set {
		caps, err := networkCaps()
		if err != nil {
			return nil, err
		}
		sc.Capabilities = &caps[0]
		sc.CapabilityCount = uint32(len(caps))
	}
	var handles [3]windows.Handle
	for i, v := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
		h, err := stdHandle(v, i > 0)
		if err != nil {
			for _, x := range handles[:i] {
				_ = windows.CloseHandle(x)
			}
			return nil, err
		}
		handles[i] = h
	}
	defer func() {
		for _, h := range handles {
			_ = windows.CloseHandle(h)
		}
	}()
	al, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer al.Delete()
	if err := al.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&sc), unsafe.Sizeof(sc)); err != nil {
		return nil, err
	}
	if err := al.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]
	si.ProcThreadAttributeList = al.List()

	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	env = setEnv(env, "TEMP", tmp)
	env = setEnv(env, "TMP", tmp)
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if cmd.SysProcAttr != nil {
		flags |= cmd.SysProcAttr.CreationFlags
	}
	cwd := cmd.Dir
	if cwd == "" {
		cwd = dir
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(utf16Ptr(cmd.Path), utf16Ptr(windows.ComposeCommandLine(cmd.Args)), nil, nil, true,
		flags, envBlock(env), utf16Ptr(cwd), &si.StartupInfo, &pi)
	if err != nil {
		return nil, fmt.Errorf("не запустить в AppContainer: %w", err)
	}
	defer closeHandle(pi.Thread)
	defer closeHandle(pi.Process)
	p, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	return p, nil
}

func isolationHint(err error) string {
	return "изоляция недоступна: " + err.Error() +
		"\nAppContainer есть в Windows 8 и новее. Запустить без изоляции: balanda sadit --bez-izolyacii ..."
}

// ProbeIzolyaciya проверяет, что профиль AppContainer создаётся.
func ProbeIzolyaciya() error {
	sid, err := appContainerSID("balanda.kamera.proverka")
	if err != nil {
		return errors.New(isolationHint(err))
	}
	if _, err := containerTemp(sid); err != nil {
		return errors.New(isolationHint(err))
	}
	return nil
}

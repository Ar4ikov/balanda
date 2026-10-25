package kamera

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status — что сейчас с камерой.
type Status string

const (
	// StatusSidit — процесс запущен и под надзором.
	StatusSidit Status = "sidit"
	// StatusVyshla — процесс завершился сам.
	StatusVyshla Status = "vyshla"
	// StatusUbita — процесс убит за превышение пайки.
	StatusUbita Status = "ubita"
	// StatusPogashena — процесс погашен командой otboy.
	StatusPogashena Status = "pogashena"
	// StatusPoteryana — надзиратель пропал, чем всё кончилось, неизвестно.
	StatusPoteryana Status = "poteryana"
)

// Title — статус по-русски.
func (s Status) Title() string {
	switch s {
	case StatusSidit:
		return "сидит"
	case StatusVyshla:
		return "вышла"
	case StatusUbita:
		return "убита"
	case StatusPogashena:
		return "погашена"
	case StatusPoteryana:
		return "потеряна"
	}
	return string(s)
}

// Причины, по которым камеру убили.
const (
	ReasonTime  = "time"
	ReasonMem   = "mem"
	ReasonCPU   = "cpu"
	ReasonOtboy = "otboy"
)

// ReasonTitle — причина по-русски.
func ReasonTitle(r string) string {
	switch r {
	case ReasonTime:
		return "вышло время"
	case ReasonMem:
		return "не хватило памяти"
	case ReasonCPU:
		return "кончилось процессорное время"
	case ReasonOtboy:
		return "отбой"
	}
	return r
}

// Движки, которыми ставятся лимиты.
const (
	EngineCgroup = "cgroup2"
	EngineRlimit = "rlimit"
	EngineJob    = "job"
	EngineNone   = "none"
)

// EngineTitle — движок по-русски.
func EngineTitle(e string) string {
	switch e {
	case EngineCgroup:
		return "cgroups v2"
	case EngineRlimit:
		if runtime.GOOS == "darwin" {
			return "rlimit + сторож памяти"
		}
		return "rlimit"
	case EngineJob:
		return "Job Object"
	case EngineNone:
		return "без лимитов"
	}
	return e
}

// Usage — итоговый расход ресурсов.
type Usage struct {
	UserMs  int64 `json:"user_ms"`
	SysMs   int64 `json:"sys_ms"`
	PeakMem int64 `json:"peak_mem"`
}

// Kamera — запись о камере в каталоге состояния.
type Kamera struct {
	ID          int        `json:"id"`
	Name        string     `json:"name,omitempty"`
	Cmd         []string   `json:"cmd"`
	Dir         string     `json:"dir"`
	Paika       Paika      `json:"paika"`
	Engine      string     `json:"engine,omitempty"`
	EngineInfo  string     `json:"engine_info,omitempty"`
	Background  bool       `json:"background,omitempty"`
	Status      Status     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	PID         int        `json:"pid,omitempty"`
	PGID        int        `json:"pgid,omitempty"`
	ProcStart   uint64     `json:"proc_start,omitempty"`
	Nadzor      int        `json:"nadzor,omitempty"`
	NadzorStart uint64     `json:"nadzor_start,omitempty"`
	Started     time.Time  `json:"started"`
	Ended       *time.Time `json:"ended,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Signal      string     `json:"signal,omitempty"`
	Error       string     `json:"error,omitempty"`
	Usage       *Usage     `json:"usage,omitempty"`
	Stdout      string     `json:"stdout,omitempty"`
	Stderr      string     `json:"stderr,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
}

// Label — «камера 3» или «камера 3 «сборка»».
func (k *Kamera) Label() string {
	if k.Name != "" {
		return fmt.Sprintf("камера %d «%s»", k.ID, k.Name)
	}
	return fmt.Sprintf("камера %d", k.ID)
}

// CmdLine — команда одной строкой.
func (k *Kamera) CmdLine() string {
	parts := make([]string, len(k.Cmd))
	for i, a := range k.Cmd {
		if a == "" || strings.ContainsAny(a, " \t\"'$\\") {
			a = strconv.Quote(a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// Elapsed — сколько камера просидела.
func (k *Kamera) Elapsed(now time.Time) time.Duration {
	end := now
	if k.Ended != nil {
		end = *k.Ended
	}
	return end.Sub(k.Started)
}

// Store — каталог с записями о камерах.
type Store struct {
	Dir string
}

// StateDir возвращает каталог состояния balanda:
//
//   - $BALANDA_STATE_DIR, если задан;
//   - $XDG_STATE_HOME/balanda, если задан XDG_STATE_HOME;
//   - ~/.local/state/balanda на Linux и других unix, кроме macOS;
//   - os.UserConfigDir()/balanda на macOS и Windows.
func StateDir() (string, error) {
	if d := os.Getenv("BALANDA_STATE_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "balanda"), nil
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "state", "balanda"), nil
		}
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("не найден каталог для состояния: %w", err)
	}
	return filepath.Join(d, "balanda"), nil
}

// OpenStore открывает (и при нужде создаёт) каталог камер.
func OpenStore() (*Store, error) {
	d, err := StateDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(d, "kamery"))
}

// NewStore открывает каталог камер по явному пути.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("не создать каталог состояния %s: %w", dir, err)
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(id int, ext string) string {
	return filepath.Join(s.Dir, strconv.Itoa(id)+ext)
}

// Reserve занимает свободный номер камеры и сохраняет черновик записи.
func (s *Store) Reserve(k *Kamera) error {
	ids, err := s.ids()
	if err != nil {
		return err
	}
	next := 1
	if len(ids) > 0 {
		next = ids[len(ids)-1] + 1
	}
	for i := 0; i < 1000; i++ {
		f, err := os.OpenFile(s.path(next, ".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			next++
			continue
		}
		if err != nil {
			return fmt.Errorf("не занять номер камеры: %w", err)
		}
		_ = f.Close()
		k.ID = next
		return s.Save(k)
	}
	return errors.New("не занять номер камеры: все номера заняты")
}

// Save атомарно записывает камеру.
func (s *Store) Save(k *Kamera) error {
	data, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, fmt.Sprintf(".%d-*.tmp", k.ID))
	if err != nil {
		return fmt.Errorf("не записать камеру %d: %w", k.ID, err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("не записать камеру %d: %w", k.ID, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := renameReplace(tmp.Name(), s.path(k.ID, ".json")); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("не записать камеру %d: %w", k.ID, err)
	}
	return nil
}

// renameReplace — os.Rename с повтором: на Windows файл может быть
// открыт на чтение соседним процессом.
func renameReplace(from, to string) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return err
}

// Load читает камеру по номеру.
func (s *Store) Load(id int) (*Kamera, error) {
	var data []byte
	var err error
	for i := 0; i < 20; i++ {
		data, err = os.ReadFile(s.path(id, ".json"))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("камеры %d нет", id)
	}
	if err != nil {
		return nil, err
	}
	var k Kamera
	if len(data) == 0 {
		return nil, fmt.Errorf("камера %d ещё заполняется", id)
	}
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("запись камеры %d испорчена: %w", id, err)
	}
	return &k, nil
}

func (s *Store) ids() ([]int, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if id, err := strconv.Atoi(name); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

// List возвращает все камеры по возрастанию номера. Испорченные
// и недописанные записи пропускаются.
func (s *Store) List() ([]*Kamera, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	var out []*Kamera
	for _, id := range ids {
		k, err := s.Load(id)
		if err != nil {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

// Find ищет камеру по номеру или имени.
func (s *Store) Find(ref string) (*Kamera, error) {
	ref = strings.TrimSpace(ref)
	if id, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil {
		return s.Load(id)
	}
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var found *Kamera
	for _, k := range all {
		if k.Name == ref {
			found = k // берём последнюю с таким именем
		}
	}
	if found == nil {
		return nil, fmt.Errorf("камеры %q нет, список: balanda pereklichka", ref)
	}
	return found, nil
}

// Remove удаляет запись и журналы камеры.
func (s *Store) Remove(k *Kamera) error {
	for _, p := range []string{k.Stdout, k.Stderr, s.path(k.ID, ".otboy")} {
		if p != "" {
			_ = os.Remove(p)
		}
	}
	return os.Remove(s.path(k.ID, ".json"))
}

// LogPaths — пути журналов для фоновой камеры.
func (s *Store) LogPaths(id int) (stdout, stderr string) {
	return s.path(id, ".out"), s.path(id, ".err")
}

// markOtboy оставляет метку: камеру гасят командой otboy.
func (s *Store) markOtboy(id int) error {
	return os.WriteFile(s.path(id, ".otboy"), []byte(time.Now().Format(time.RFC3339)), 0o600)
}

// takeOtboy проверяет и снимает метку отбоя.
func (s *Store) takeOtboy(id int) bool {
	err := os.Remove(s.path(id, ".otboy"))
	return err == nil
}

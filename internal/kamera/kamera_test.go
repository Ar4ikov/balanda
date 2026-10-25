package kamera

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// Тестовый бинарник служит и надзирателем (__nadziratel, __zek),
// и подопытным процессом: что делать, говорит BALANDA_TEST_HELPER.
func TestMain(m *testing.M) {
	if code, ok := Dispatch(os.Args); ok {
		os.Exit(code)
	}
	if mode := os.Getenv("BALANDA_TEST_HELPER"); mode != "" {
		os.Exit(helper(mode))
	}
	os.Exit(m.Run())
}

func helper(mode string) int {
	switch mode {
	case "exit3":
		return 3
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	case "spin":
		deadline := time.Now().Add(30 * time.Second)
		x := 0
		for time.Now().Before(deadline) {
			x++
		}
		return x & 1
	case "alloc":
		var keep [][]byte
		for i := 0; i < 64; i++ {
			b := make([]byte, 8<<20)
			for j := range b {
				b[j] = byte(j)
			}
			keep = append(keep, b)
			time.Sleep(20 * time.Millisecond)
		}
		fmt.Println(len(keep))
		time.Sleep(5 * time.Second)
		return 0
	}
	return 99
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func helperCmd(t *testing.T, mode string) []string {
	t.Helper()
	t.Setenv("BALANDA_TEST_HELPER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe}
}

func run(t *testing.T, s *Store, mode string, p Paika) (*Kamera, int) {
	t.Helper()
	k, code, err := Sadit(s, RunOptions{
		Cmd:    helperCmd(t, mode),
		Paika:  p,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	k, err = s.Load(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	return k, code
}

func TestStoreReserveFind(t *testing.T) {
	s := testStore(t)
	a := &Kamera{Name: "a", Cmd: []string{"x"}, Status: StatusVyshla, Started: time.Now()}
	b := &Kamera{Name: "b", Cmd: []string{"y"}, Status: StatusSidit, Started: time.Now()}
	if err := s.Reserve(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(b); err != nil {
		t.Fatal(err)
	}
	if a.ID != 1 || b.ID != 2 {
		t.Fatalf("номера %d и %d, ждали 1 и 2", a.ID, b.ID)
	}
	for ref, want := range map[string]int{"1": 1, "#2": 2, "b": 2, "a": 1} {
		k, err := s.Find(ref)
		if err != nil {
			t.Fatalf("Find(%q): %v", ref, err)
		}
		if k.ID != want {
			t.Errorf("Find(%q) = %d, ждали %d", ref, k.ID, want)
		}
	}
	if _, err := s.Find("нет-такой"); err == nil {
		t.Error("Find несуществующей камеры: ждали ошибку")
	}
	if err := s.Remove(a); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != 2 {
		t.Fatalf("после Remove осталось %d записей", len(all))
	}
	c := &Kamera{Cmd: []string{"z"}, Started: time.Now()}
	if err := s.Reserve(c); err != nil {
		t.Fatal(err)
	}
	if c.ID != 3 {
		t.Errorf("новый номер %d, ждали 3", c.ID)
	}
}

func TestSaditExitCode(t *testing.T) {
	s := testStore(t)
	k, code := run(t, s, "exit3", Paika{})
	if code != 3 {
		t.Fatalf("код выхода %d, ждали 3", code)
	}
	if k.Status != StatusVyshla || k.ExitCode == nil || *k.ExitCode != 3 {
		t.Fatalf("статус %s, код %v", k.Status, k.ExitCode)
	}
	if k.PID == 0 || k.Ended == nil {
		t.Fatal("надзиратель не записал PID или время выхода")
	}
}

func TestSaditNotFound(t *testing.T) {
	s := testStore(t)
	k, code, err := Sadit(s, RunOptions{Cmd: []string{"balanda-net-takoy-komandy"}, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if code != ExitNoStart {
		t.Fatalf("код %d, ждали %d", code, ExitNoStart)
	}
	if k.Error == "" {
		t.Fatal("ошибка запуска не записана")
	}
}

func TestSaditTimeLimit(t *testing.T) {
	s := testStore(t)
	start := time.Now()
	k, code := run(t, s, "sleep", Paika{Time: 700 * time.Millisecond})
	if code != ExitTimeout {
		t.Fatalf("код %d, ждали %d", code, ExitTimeout)
	}
	if k.Status != StatusUbita || k.Reason != ReasonTime {
		t.Fatalf("статус %s, причина %q", k.Status, k.Reason)
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Fatalf("камеру гасили %s", el)
	}
}

func TestSaditCPULimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("на Windows cpu — потолок доли, процесс не убивается")
	}
	if os.Getenv("BALANDA_ENGINE") == EngineCgroup {
		t.Skip("в cgroup cpu — доля ядра, процесс не убивается")
	}
	s := testStore(t)
	// cpu × time = 1 с процессорного времени, а time с запасом.
	k, _ := run(t, s, "spin", Paika{CPU: 0.1, Time: 10 * time.Second})
	if k.Engine == EngineCgroup {
		t.Skip("достался движок cgroup")
	}
	if k.Status != StatusUbita || k.Reason != ReasonCPU {
		t.Fatalf("статус %s, причина %q, сигнал %q", k.Status, k.Reason, k.Signal)
	}
}

func TestSaditMemLimit(t *testing.T) {
	switch runtime.GOOS {
	case "darwin":
		s := testStore(t)
		k, _ := run(t, s, "alloc", Paika{Mem: 128 << 20, Time: 20 * time.Second})
		if k.Status != StatusUbita || k.Reason != ReasonMem {
			t.Fatalf("статус %s, причина %q", k.Status, k.Reason)
		}
	case "windows":
		s := testStore(t)
		_, code := run(t, s, "alloc", Paika{Mem: 128 << 20, Time: 20 * time.Second})
		if code == 0 {
			t.Fatal("процесс выжил с 512 МБ при лимите 128 МБ")
		}
	case "linux":
		// Go-бинарнику RLIMIT_AS в 128 МБ мало даже на старт, поэтому
		// проверяем на python3, если он есть.
		py, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("нет python3")
		}
		s := testStore(t)
		k, code, err := Sadit(s, RunOptions{
			Cmd:    []string{py, "-c", "b = bytearray(512 * 1024 * 1024)"},
			Paika:  Paika{Mem: 128 << 20},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if err != nil {
			t.Fatal(err)
		}
		if code == 0 {
			t.Fatalf("python выжил с 512 МБ при лимите 128 МБ, движок %s", k.Engine)
		}
	default:
		t.Skip("не проверяем на", runtime.GOOS)
	}
}

func TestBackgroundOtboy(t *testing.T) {
	s := testStore(t)
	k, _, err := Sadit(s, RunOptions{
		Name:       "fon",
		Cmd:        helperCmd(t, "sleep"),
		Paika:      Paika{Time: time.Minute},
		Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if k.PID == 0 {
		t.Fatal("фоновая камера без PID")
	}
	procAlive, nadzorAlive := Check(s, k)
	if !procAlive || !nadzorAlive {
		t.Fatalf("процесс жив: %v, надзиратель жив: %v", procAlive, nadzorAlive)
	}
	st, err := LiveStats(k)
	if err != nil {
		t.Fatalf("LiveStats: %v", err)
	}
	if len(st.Procs) == 0 {
		t.Error("в аудите нет процессов")
	}
	if err := Otboy(s, k); err != nil {
		t.Fatal(err)
	}
	cur, err := s.Load(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != StatusPogashena {
		t.Fatalf("после отбоя статус %s", cur.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for Alive(k.PID, k.ProcStart) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if Alive(k.PID, k.ProcStart) {
		t.Fatal("процесс пережил отбой")
	}
}

func TestCheckLost(t *testing.T) {
	s := testStore(t)
	k := &Kamera{
		Cmd:     []string{"x"},
		Status:  StatusSidit,
		PID:     999999,
		Nadzor:  999998,
		Started: time.Now().Add(-time.Minute),
	}
	if err := s.Reserve(k); err != nil {
		t.Fatal(err)
	}
	Check(s, k)
	cur, err := s.Load(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != StatusPoteryana {
		t.Fatalf("статус %s, ждали «потеряна»", cur.Status)
	}
}

package kamera

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestCgroupEngine запускается только с BALANDA_ENGINE=cgroup2: в CI его
// гоняют под root на ubuntu, где есть cgroups v2.
func TestCgroupEngine(t *testing.T) {
	if os.Getenv("BALANDA_ENGINE") != EngineCgroup {
		t.Skip("нужен BALANDA_ENGINE=cgroup2")
	}
	s := testStore(t)
	k, _, err := Sadit(s, RunOptions{
		Cmd:        helperCmd(t, "sleep"),
		Paika:      Paika{CPU: 0.5, Mem: 256 << 20, Time: time.Minute},
		Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if k.Engine != EngineCgroup {
		t.Fatalf("движок %q, предупреждения: %v", k.Engine, k.Warnings)
	}
	for file, want := range map[string]string{"cpu.max": "50000 100000", "memory.max": "268435456"} {
		data, err := os.ReadFile(k.EngineInfo + "/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got != want {
			t.Errorf("%s = %q, ждали %q", file, got, want)
		}
	}
	st, err := LiveStats(k)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Source, "cgroup") || len(st.Procs) == 0 || st.Mem <= 0 {
		t.Errorf("аудит: источник %q, процессов %d, память %d", st.Source, len(st.Procs), st.Mem)
	}
	if err := Otboy(s, k); err != nil {
		t.Fatal(err)
	}
	if k.Status != StatusPogashena {
		t.Fatalf("статус %s", k.Status)
	}
	if _, err := os.Stat(k.EngineInfo); !os.IsNotExist(err) {
		t.Errorf("cgroup %s осталась после отбоя", k.EngineInfo)
	}
}

func TestCgroupOOM(t *testing.T) {
	if os.Getenv("BALANDA_ENGINE") != EngineCgroup {
		t.Skip("нужен BALANDA_ENGINE=cgroup2")
	}
	s := testStore(t)
	k, code := run(t, s, "alloc", Paika{Mem: 128 << 20, Time: 30 * time.Second})
	if k.Status != StatusUbita || k.Reason != ReasonMem {
		t.Fatalf("статус %s, причина %q, код %d", k.Status, k.Reason, code)
	}
}

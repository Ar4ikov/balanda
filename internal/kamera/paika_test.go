package kamera

import (
	"strings"
	"testing"
	"time"
)

func TestParsePaika(t *testing.T) {
	tests := []struct {
		in   string
		want Paika
	}{
		{"", Paika{}},
		{"   ", Paika{}},
		{"cpu=1,mem=256m,time=30s", Paika{CPU: 1, Mem: 256 << 20, Time: 30 * time.Second}},
		{"cpu=0.5", Paika{CPU: 0.5}},
		{"CPU=2, MEM=1G , TIME=1m30s", Paika{CPU: 2, Mem: 1 << 30, Time: 90 * time.Second}},
		{"mem=512MiB", Paika{Mem: 512 << 20}},
		{"mem=2gb", Paika{Mem: 2 << 30}},
		{"mem=1048576", Paika{Mem: 1 << 20}},
		{"mem=1.5g", Paika{Mem: 3 << 29}},
		{"time=45", Paika{Time: 45 * time.Second}},
		{"time=0.5", Paika{Time: 500 * time.Millisecond}},
		{"time=2h", Paika{Time: 2 * time.Hour}},
		{"cpu=1,,time=1s", Paika{CPU: 1, Time: time.Second}},
	}
	for _, tt := range tests {
		got, err := ParsePaika(tt.in)
		if err != nil {
			t.Errorf("ParsePaika(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParsePaika(%q) = %+v, ждали %+v", tt.in, got, tt.want)
		}
	}
}

func TestParsePaikaErrors(t *testing.T) {
	tests := []struct {
		in, wantMsg string
	}{
		{"cpu", "нет знака"},
		{"cpu=", "пустое значение"},
		{"cpu=abc", "не число"},
		{"cpu=0", "больше нуля"},
		{"cpu=-1", "больше нуля"},
		// Запятая разделяет пары, поэтому «cpu=0,5» — это cpu=0 и «5».
		{"cpu=0,5", "больше нуля"},
		{"cpu=NaN", "не число"},
		{"cpu=5000", "не бывает"},
		{"mem=256x", "непонятная единица"},
		{"mem=m", "нет числа"},
		{"mem=100k", "меньше 1 МБ"},
		{"mem=1.2.3m", "не число"},
		{"time=soon", "не длительность"},
		{"time=-5s", "больше нуля"},
		{"time=0", "больше нуля"},
		{"disk=1g", "неизвестный ключ"},
		{"cpu=1,cpu=2", "дважды"},
	}
	for _, tt := range tests {
		_, err := ParsePaika(tt.in)
		if err == nil {
			t.Errorf("ParsePaika(%q): ждали ошибку", tt.in)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantMsg) {
			t.Errorf("ParsePaika(%q): ошибка %q, ждали в ней %q", tt.in, err, tt.wantMsg)
		}
	}
}

func TestPaikaStringRoundTrip(t *testing.T) {
	for _, in := range []string{"cpu=1,mem=256m,time=30s", "cpu=0.25", "mem=3g", "time=1m30s", "mem=1536k"} {
		p, err := ParsePaika(in)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParsePaika(p.String())
		if err != nil {
			t.Fatalf("%q → %q: %v", in, p.String(), err)
		}
		if again != p {
			t.Errorf("%q → %q → %+v, ждали %+v", in, p.String(), again, p)
		}
	}
	if s := (Paika{}).String(); s != "без лимитов" {
		t.Errorf("пустая пайка: %q", s)
	}
}

func TestCPUSeconds(t *testing.T) {
	tests := []struct {
		p    Paika
		want uint64
	}{
		{Paika{}, 0},
		{Paika{CPU: 1}, 0},
		{Paika{Time: time.Second}, 0},
		{Paika{CPU: 1, Time: 30 * time.Second}, 30},
		{Paika{CPU: 0.5, Time: 3 * time.Second}, 2},
		{Paika{CPU: 2, Time: 1500 * time.Millisecond}, 3},
	}
	for _, tt := range tests {
		if got := tt.p.CPUSeconds(); got != tt.want {
			t.Errorf("%+v.CPUSeconds() = %d, ждали %d", tt.p, got, tt.want)
		}
	}
}

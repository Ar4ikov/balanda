// Package kamera — песочница: запуск процесса с ограниченной «пайкой»
// ресурсов, учёт камер и аудит.
package kamera

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Paika — лимиты камеры. Нулевое значение поля значит «без лимита».
type Paika struct {
	// CPU — сколько ядер можно занять, например 0.5 или 2.
	CPU float64 `json:"cpu,omitempty"`
	// Mem — потолок памяти в байтах.
	Mem int64 `json:"mem,omitempty"`
	// Time — сколько процесс может просидеть по настенным часам.
	Time time.Duration `json:"time,omitempty"`
}

// Empty сообщает, что лимитов нет.
func (p Paika) Empty() bool { return p.CPU == 0 && p.Mem == 0 && p.Time == 0 }

// String возвращает пайку в том же виде, в каком её принимает ParsePaika.
func (p Paika) String() string {
	var parts []string
	if p.CPU > 0 {
		parts = append(parts, "cpu="+strconv.FormatFloat(p.CPU, 'f', -1, 64))
	}
	if p.Mem > 0 {
		parts = append(parts, "mem="+formatMem(p.Mem))
	}
	if p.Time > 0 {
		parts = append(parts, "time="+p.Time.String())
	}
	if len(parts) == 0 {
		return "без лимитов"
	}
	return strings.Join(parts, ",")
}

// CPUSeconds — бюджет процессорного времени для rlimit: cpu × time,
// округлённый вверх. Ноль, если чего-то из двух нет.
func (p Paika) CPUSeconds() uint64 {
	if p.CPU <= 0 || p.Time <= 0 {
		return 0
	}
	return uint64(math.Ceil(p.CPU * p.Time.Seconds()))
}

// ParsePaika разбирает строку вида «cpu=1,mem=256m,time=30s».
//
//   - cpu — число ядер больше нуля: 1, 0.5, 2;
//   - mem — байты с необязательным суффиксом k, m, g, t (степени 1024),
//     можно kb/mb/gb и kib/mib/gib, регистр не важен;
//   - time — длительность Go (30s, 5m, 1h30m) или число секунд.
//
// Пустая строка значит «без лимитов».
func ParsePaika(s string) (Paika, error) {
	var p Paika
	s = strings.TrimSpace(s)
	if s == "" {
		return p, nil
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			return Paika{}, fmt.Errorf("пайка: в %q нет знака «=», нужно ключ=значение", part)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if val == "" {
			return Paika{}, fmt.Errorf("пайка: у %q пустое значение", key)
		}
		if seen[key] {
			return Paika{}, fmt.Errorf("пайка: %q указан дважды", key)
		}
		seen[key] = true
		switch key {
		case "cpu":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return Paika{}, fmt.Errorf("пайка: cpu=%q — не число", val)
			}
			if f <= 0 {
				return Paika{}, fmt.Errorf("пайка: cpu должен быть больше нуля, а не %s", val)
			}
			if f > 1024 {
				return Paika{}, fmt.Errorf("пайка: cpu=%s — столько ядер не бывает", val)
			}
			p.CPU = f
		case "mem":
			n, err := parseMem(val)
			if err != nil {
				return Paika{}, err
			}
			p.Mem = n
		case "time":
			d, err := parseTime(val)
			if err != nil {
				return Paika{}, err
			}
			p.Time = d
		default:
			return Paika{}, fmt.Errorf("пайка: неизвестный ключ %q, есть только cpu, mem и time", key)
		}
	}
	return p, nil
}

func parseMem(val string) (int64, error) {
	v := strings.ToLower(strings.TrimSpace(val))
	i := strings.IndexFunc(v, func(r rune) bool { return !(unicode.IsDigit(r) || r == '.') })
	num, suf := v, ""
	if i >= 0 {
		num, suf = v[:i], strings.TrimSpace(v[i:])
	}
	if num == "" {
		return 0, fmt.Errorf("пайка: mem=%q — нет числа", val)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("пайка: mem=%q — не число", val)
	}
	mult := map[string]float64{
		"": 1, "b": 1,
		"k": 1 << 10, "kb": 1 << 10, "kib": 1 << 10,
		"m": 1 << 20, "mb": 1 << 20, "mib": 1 << 20,
		"g": 1 << 30, "gb": 1 << 30, "gib": 1 << 30,
		"t": 1 << 40, "tb": 1 << 40, "tib": 1 << 40,
	}
	m, ok := mult[suf]
	if !ok {
		return 0, fmt.Errorf("пайка: mem=%q — непонятная единица %q, бывают k, m, g, t", val, suf)
	}
	n := f * m
	if n < 1<<20 {
		return 0, fmt.Errorf("пайка: mem=%s — меньше 1 МБ процесс даже не запустится", val)
	}
	if n > math.MaxInt64/2 {
		return 0, fmt.Errorf("пайка: mem=%s — слишком много", val)
	}
	return int64(n), nil
}

func parseTime(val string) (time.Duration, error) {
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		if f <= 0 {
			return 0, fmt.Errorf("пайка: time должен быть больше нуля, а не %s", val)
		}
		return time.Duration(f * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("пайка: time=%q — не длительность, пишите 30s, 5m или 1h", val)
	}
	if d <= 0 {
		return 0, fmt.Errorf("пайка: time должен быть больше нуля, а не %s", val)
	}
	return d, nil
}

func formatMem(n int64) string {
	switch {
	case n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + "g"
	case n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "m"
	case n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// Package vyvod содержит мелочи для вывода в терминал: цвета, размеры,
// таблицы и вопрос «да/нет».
package vyvod

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// Цвета ANSI. Пустые строки, если цвет выключен.
type Palette struct {
	Reset, Bold, Dim, Red, Green, Yellow, Blue, Magenta, Cyan, Underline string
}

var plain = Palette{}

var ansi = Palette{
	Reset:     "\x1b[0m",
	Bold:      "\x1b[1m",
	Dim:       "\x1b[2m",
	Red:       "\x1b[31m",
	Green:     "\x1b[32m",
	Yellow:    "\x1b[33m",
	Blue:      "\x1b[34m",
	Magenta:   "\x1b[35m",
	Cyan:      "\x1b[36m",
	Underline: "\x1b[4m",
}

// Colors возвращает палитру для w: цвет есть, только если w — терминал
// и не задана переменная NO_COLOR. FORCE_COLOR включает цвет принудительно.
func Colors(w io.Writer) Palette {
	if os.Getenv("NO_COLOR") != "" {
		return plain
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return ansi
	}
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return ansi
	}
	return plain
}

// IsTerminal сообщает, подключён ли файл к терминалу.
func IsTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// Width возвращает ширину терминала или def, если её не узнать.
func Width(def int) int {
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 20 {
		return c
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return def
}

// Size печатает размер в байтах по-русски: «1,5 ГБ».
func Size(n int64) string {
	if n < 0 {
		return "—"
	}
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[0])
	}
	var s string
	if f >= 100 {
		s = strconv.FormatFloat(math.Round(f), 'f', 0, 64)
	} else {
		s = strconv.FormatFloat(f, 'f', 1, 64)
		s = strings.TrimSuffix(s, ".0")
	}
	return strings.Replace(s, ".", ",", 1) + " " + units[i]
}

// Plural выбирает форму слова по числу: Plural(5, "файл", "файла", "файлов").
func Plural(n int64, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	n10, n100 := n%10, n%100
	switch {
	case n10 == 1 && n100 != 11:
		return one
	case n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14):
		return few
	default:
		return many
	}
}

// Table печатает выровненную таблицу. Ширина считается в рунах,
// ANSI-последовательности в подсчёт не входят.
type Table struct {
	Header []string
	Rows   [][]string
	// Right — номера колонок, выровненных по правому краю.
	Right map[int]bool
}

func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// Truncate обрезает строку до max рун, добавляя многоточие.
func Truncate(s string, max int) string {
	if max <= 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// Write выводит таблицу в w.
func (t Table) Write(w io.Writer, p Palette) {
	cols := len(t.Header)
	for _, r := range t.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	measure := func(row []string) {
		for i, c := range row {
			if l := visibleLen(c); l > widths[i] {
				widths[i] = l
			}
		}
	}
	measure(t.Header)
	for _, r := range t.Rows {
		measure(r)
	}
	line := func(row []string, pre, post string) {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			c := ""
			if i < len(row) {
				c = row[i]
			}
			pad := strings.Repeat(" ", widths[i]-visibleLen(c))
			if i > 0 {
				b.WriteString("  ")
			}
			if t.Right[i] {
				b.WriteString(pad + c)
			} else if i < cols-1 {
				b.WriteString(c + pad)
			} else {
				b.WriteString(c)
			}
		}
		fmt.Fprintln(w, pre+strings.TrimRight(b.String(), " ")+post)
	}
	if len(t.Header) > 0 {
		line(t.Header, p.Bold, p.Reset)
	}
	for _, r := range t.Rows {
		line(r, "", "")
	}
}

// Confirm задаёт вопрос «да/нет» и читает ответ из in.
// Да — «д», «да», «y», «yes» в любом регистре, всё остальное — нет.
// «l» тоже да: это клавиша «д», нажатая в английской раскладке.
func Confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [д/Н] ", question)
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "д", "да", "y", "yes", "l":
		return true
	}
	return false
}

// Duration печатает длительность коротко: «850 мс», «1,2 с», «3 мин 5 с».
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d мс", d.Milliseconds())
	case d < 10*time.Second:
		s := strconv.FormatFloat(d.Seconds(), 'f', 1, 64)
		return strings.Replace(strings.TrimSuffix(s, ".0"), ".", ",", 1) + " с"
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		d = d.Round(time.Second)
		m, s := int(d.Minutes()), int(d.Seconds())%60
		if s == 0 {
			return fmt.Sprintf("%d мин", m)
		}
		return fmt.Sprintf("%d мин %d с", m, s)
	default:
		d = d.Round(time.Minute)
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%d ч", h)
		}
		return fmt.Sprintf("%d ч %d мин", h, m)
	}
}

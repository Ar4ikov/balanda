package voda

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// token — слово в тексте.
type token struct {
	norm       string // в нижнем регистре, «ё» → «е»
	start, end int    // байтовые смещения в исходном тексте
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}

// tokenize режет текст на слова. Дефис внутри слова сохраняется:
// «по-настоящему» — одно слово.
func tokenize(text string) []token {
	var out []token
	start := -1
	for i, r := range text {
		letter := unicode.IsLetter(r)
		if r == '-' && start >= 0 {
			// дефис оставляем, если дальше буква
			next, _ := utf8.DecodeRuneInString(text[i+1:])
			letter = unicode.IsLetter(next)
		}
		switch {
		case letter && start < 0:
			start = i
		case !letter && start >= 0:
			out = append(out, token{norm: normalize(text[start:i]), start: start, end: i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, token{norm: normalize(text[start:]), start: start, end: len(text)})
	}
	return out
}

// pattern — скомпилированный паттерн из словаря.
type pattern struct {
	src   string
	cat   Category
	words []string // «*» — любое слово, «x*» — префикс
}

func (p *pattern) match(toks []token, i int) bool {
	if i+len(p.words) > len(toks) {
		return false
	}
	for j, w := range p.words {
		t := toks[i+j].norm
		switch {
		case w == "*":
		case strings.HasSuffix(w, "*"):
			if !strings.HasPrefix(t, w[:len(w)-1]) {
				return false
			}
		default:
			if t != w {
				return false
			}
		}
	}
	return true
}

var patterns = func() []pattern {
	var out []pattern
	add := func(list []string, c Category) {
		for _, s := range list {
			out = append(out, pattern{src: s, cat: c, words: strings.Fields(normalize(s))})
		}
	}
	add(lexShtamp, CatShtamp)
	add(lexKancelyarit, CatKancelyarit)
	add(lexOcenka, CatOcenka)
	add(lexZhargon, CatZhargon)
	// Длинные паттерны проверяются первыми.
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].words) > len(out[j].words) })
	return out
}()

// Отглагольное существительное: -ание, -ение, -ация и их падежи.
var verbalRe = regexp.MustCompile(`^\p{L}{2,}(ани|ени|яни|ировани|овани)(е|я|ю|ем|и|й|ям|ями|ях|ий)$|^\p{L}{3,}(аци|яци)(я|и|ю|ей|ям|ями|ях)$`)

func isVerbal(norm string) bool {
	return verbalRe.MatchString(norm) && !lexVerbalStop[norm]
}

// Finding — одна находка.
type Finding struct {
	Cat        Category
	Text       string // как в тексте
	Key        string // паттерн словаря или «цепочка»
	Start, End int
	Line       int
}

// Report — итог проверки.
type Report struct {
	Score    int
	Words    int
	Verbal   int // отглагольных существительных
	Findings []Finding
	Counts   map[Category]int
	Text     string
}

// Verdict — словесная оценка по шкале.
func (r *Report) Verdict() string {
	switch {
	case r.Score < 20:
		return "сухо"
	case r.Score < 40:
		return "влажно"
	case r.Score < 60:
		return "сыро"
	case r.Score < 80:
		return "болото"
	default:
		return "потоп"
	}
}

// VerbalPer100 — отглагольных существительных на 100 слов.
func (r *Report) VerbalPer100() float64 {
	if r.Words == 0 {
		return 0
	}
	return float64(r.Verbal) * 100 / float64(r.Words)
}

// Top — самые частые находки категории: «в рамках ×3».
func (r *Report) Top(c Category, n int) []string {
	count := map[string]int{}
	var order []string
	for _, f := range r.Findings {
		if f.Cat != c {
			continue
		}
		k := normalize(f.Text)
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	if len(order) > n {
		order = order[:n]
	}
	out := make([]string, len(order))
	for i, k := range order {
		out[i] = k
		if count[k] > 1 {
			out[i] += " ×" + strconv.Itoa(count[k])
		}
	}
	return out
}

// Нормальная для живого текста доля отглагольных существительных
// на 100 слов. Всё, что выше, добавляет воды.
const verbalNorm = 4.0

// Proba оценивает водность текста от 0 до 100.
func Proba(text string) *Report {
	toks := tokenize(text)
	r := &Report{Words: len(toks), Counts: map[Category]int{}, Text: text}

	covered := make([]bool, len(toks))
	for i := 0; i < len(toks); i++ {
		if covered[i] {
			continue
		}
		for pi := range patterns {
			p := &patterns[pi]
			if !p.match(toks, i) {
				continue
			}
			end := i + len(p.words) - 1
			r.Findings = append(r.Findings, Finding{
				Cat: p.cat, Key: p.src,
				Start: toks[i].start, End: toks[end].end,
				Text: text[toks[i].start:toks[end].end],
			})
			for j := i; j <= end; j++ {
				covered[j] = true
			}
			break
		}
	}

	// Цепочки: три и больше отглагольных существительных подряд, между
	// ними не больше одного другого слова и нет знаков препинания.
	verbal := make([]bool, len(toks))
	for i, t := range toks {
		verbal[i] = isVerbal(t.norm)
		if verbal[i] {
			r.Verbal++
		}
	}
	for i := 0; i < len(toks); {
		if !verbal[i] {
			i++
			continue
		}
		n, last := 1, i
		for k := i + 1; k < len(toks) && k <= last+2; k++ {
			if hasPunct(text[toks[k-1].end:toks[k].start]) {
				break
			}
			if verbal[k] {
				n++
				last = k
			}
		}
		if n >= 3 {
			r.Findings = append(r.Findings, Finding{
				Cat: CatCepochka, Key: "цепочка",
				Start: toks[i].start, End: toks[last].end,
				Text: text[toks[i].start:toks[last].end],
			})
			i = last + 1
			continue
		}
		i++
	}

	sort.SliceStable(r.Findings, func(i, j int) bool { return r.Findings[i].Start < r.Findings[j].Start })
	line, pos := 1, 0
	for i := range r.Findings {
		line += strings.Count(text[pos:r.Findings[i].Start], "\n")
		pos = r.Findings[i].Start
		r.Findings[i].Line = line
		r.Counts[r.Findings[i].Cat]++
	}
	r.Score = score(r)
	return r
}

func hasPunct(between string) bool {
	return strings.ContainsAny(between, ".,;:!?()—–\"«»\n")
}

func score(r *Report) int {
	if r.Words == 0 {
		return 0
	}
	var w float64
	for _, f := range r.Findings {
		w += f.Cat.weight()
	}
	per100 := w * 100 / float64(r.Words)
	if excess := r.VerbalPer100() - verbalNorm; excess > 0 {
		per100 += excess * 0.5
	}
	s := 100 * (1 - math.Exp(-per100/10))
	return int(math.Round(s))
}

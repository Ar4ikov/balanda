package voda

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestStili(t *testing.T) {
	ids := StilIDs()
	want := []string{"gosuslugi", "korporat", "nauka"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("стили %v, ждали %v", ids, want)
	}
	for _, id := range ids {
		s, err := GetStil(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Title == "" || len(s.Templates()) < 5 {
			t.Errorf("%s: название %q, шаблонов %d", id, s.Title, len(s.Templates()))
		}
	}
	if _, err := GetStil("pravda"); err == nil {
		t.Error("несуществующий стиль: ждали ошибку")
	}
}

func TestParseStilErrors(t *testing.T) {
	bad := map[string]string{
		"нет шаблонов":   "@a\nx\n",
		"пустой раздел":  "@шаблоны\n{b}\n@b\n",
		"нет раздела":    "@шаблоны\n{c}\n",
		"текст до @":     "x\n@шаблоны\ny\n",
		"повтор раздела": "@шаблоны\ny\n@шаблоны\nz\n",
	}
	for name, data := range bad {
		if _, err := parseStil("test", data); err == nil {
			t.Errorf("%s: ждали ошибку", name)
		}
	}
}

func TestGeneratorDeterministic(t *testing.T) {
	s, err := GetStil("gosuslugi")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewGenerator(s, 42).Paragraphs(3)
	b, _ := NewGenerator(s, 42).Paragraphs(3)
	c, _ := NewGenerator(s, 43).Paragraphs(3)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Error("одно зерно дало разный текст")
	}
	if strings.Join(a, "\n") == strings.Join(c, "\n") {
		t.Error("разные зёрна дали одинаковый текст")
	}
	if len(a) != 3 {
		t.Errorf("абзацев %d, ждали 3", len(a))
	}
	if _, err := NewGenerator(s, 1).Paragraphs(0); err == nil {
		t.Error("0 абзацев: ждали ошибку")
	}
}

// Каждое предложение собрано целиком: без пропущенных подстановок,
// с заглавной буквы, с точкой в конце, без двойных пробелов.
func TestGeneratorShape(t *testing.T) {
	for _, id := range StilIDs() {
		s, _ := GetStil(id)
		for seed := uint64(0); seed < 200; seed++ {
			g := NewGenerator(s, seed)
			sent := g.Sentence(map[string]bool{})
			if strings.ContainsAny(sent, "{}") {
				t.Fatalf("%s/%d: подстановка не раскрыта: %q", id, seed, sent)
			}
			r, _ := utf8.DecodeRuneInString(sent)
			if !unicode.IsUpper(r) {
				t.Fatalf("%s/%d: не с заглавной: %q", id, seed, sent)
			}
			if !strings.HasSuffix(sent, ".") || strings.HasSuffix(sent, "..") {
				t.Fatalf("%s/%d: конец предложения: %q", id, seed, sent)
			}
			for _, bad := range []string{"  ", " ,", ",,", ",.", " ."} {
				if strings.Contains(sent, bad) {
					t.Fatalf("%s/%d: %q в %q", id, seed, bad, sent)
				}
			}
		}
	}
}

func TestWrap(t *testing.T) {
	text := strings.Repeat("слово ", 50)
	for _, line := range strings.Split(Wrap(text, 30), "\n") {
		if n := utf8.RuneCountInString(line); n > 30 {
			t.Fatalf("строка длиной %d: %q", n, line)
		}
	}
	if Wrap("а б", 0) != "а б" {
		t.Error("ширина 0 должна оставлять текст как есть")
	}
}

func TestTokenize(t *testing.T) {
	toks := tokenize("По-настоящему, Ёжик! 42 - тире x-")
	var got []string
	for _, tk := range toks {
		got = append(got, tk.norm)
	}
	want := "по-настоящему ежик тире x"
	if strings.Join(got, " ") != want {
		t.Fatalf("токены %q, ждали %q", strings.Join(got, " "), want)
	}
}

func find(r *Report, text string) *Finding {
	for i := range r.Findings {
		if normalize(r.Findings[i].Text) == normalize(text) {
			return &r.Findings[i]
		}
	}
	return nil
}

func TestProbaFindings(t *testing.T) {
	text := "В рамках проекта данный модуль является ключевым.\n" +
		"Стоит отметить, что в конечном счёте мы подсветили импакт для стейкхолдеров.\n" +
		"Цель — обеспечение повышения эффективности использования ресурсов.\n" +
		"Это играет важную роль."
	r := Proba(text)
	cases := []struct {
		text string
		cat  Category
		line int
	}{
		{"В рамках", CatKancelyarit, 1},
		{"данный", CatKancelyarit, 1},
		{"является", CatKancelyarit, 1},
		{"ключевым", CatShtamp, 1},
		{"Стоит отметить", CatShtamp, 2},
		{"в конечном счёте", CatShtamp, 2},
		{"импакт", CatZhargon, 2},
		{"стейкхолдеров", CatZhargon, 2},
		{"обеспечение повышения эффективности использования", CatCepochka, 3},
		{"играет важную роль", CatShtamp, 4},
	}
	for _, c := range cases {
		f := find(r, c.text)
		if f == nil {
			t.Errorf("не нашли %q", c.text)
			continue
		}
		if f.Cat != c.cat || f.Line != c.line {
			t.Errorf("%q: категория %v, строка %d; ждали %v, строку %d", c.text, f.Cat, f.Line, c.cat, c.line)
		}
		if text[f.Start:f.End] != f.Text {
			t.Errorf("%q: смещения не совпадают с текстом", c.text)
		}
	}
}

func TestProbaNoFalseHits(t *testing.T) {
	// «данные», «мощность» и цепочка, разорванная запятыми, — не находки.
	r := Proba("Данные с датчика показали мощность 5 кВт. Решения, изменения, обновления пришли вовремя.")
	if len(r.Findings) != 0 {
		for _, f := range r.Findings {
			t.Errorf("лишняя находка: %q (%s)", f.Text, f.Cat.Title())
		}
	}
}

func TestProbaEmpty(t *testing.T) {
	r := Proba("   \n")
	if r.Score != 0 || r.Words != 0 {
		t.Fatalf("пустой текст: оценка %d, слов %d", r.Score, r.Words)
	}
}

var humanTexts = []string{
	`Вчера выкатили релиз в пятницу вечером. Тимлид сказал, что всё проверено, и ушёл домой. Через час упала база: миграция удалила не ту колонку. Дежурный полночи откатывал схему и пил холодный кофе. Утром все сделали вид, что так и задумано.`,
	`Мы переехали на новый сервер в субботу. Сначала забыли перенести крон, потом выяснилось, что диск меньше старого. В понедельник бухгалтерия спросила, куда делись отчёты. Отчёты нашлись в корзине у админа, который чистил место.`,
	`Если тест падает раз в неделю, его никто не чинит. Его перезапускают. Через год перезапуск становится частью сборки, а тест — частью фольклора. Новички спрашивают, зачем он нужен, и слышат: так исторически сложилось.`,
}

func TestProbaWaterScoresHigh(t *testing.T) {
	for _, id := range StilIDs() {
		s, _ := GetStil(id)
		for seed := uint64(1); seed <= 30; seed++ {
			ps, _ := NewGenerator(s, seed).Paragraphs(3)
			r := Proba(strings.Join(ps, "\n\n"))
			if r.Score < 60 {
				t.Errorf("%s/%d: вода оценена в %d", id, seed, r.Score)
			}
		}
	}
	for i, txt := range humanTexts {
		if r := Proba(txt); r.Score > 25 {
			t.Errorf("живой текст %d оценён в %d: %v", i, r.Score, r.Top(CatKancelyarit, 5))
		}
	}
}

func TestVerdictAndTop(t *testing.T) {
	r := &Report{Score: 85, Findings: []Finding{
		{Cat: CatKancelyarit, Text: "в рамках"}, {Cat: CatKancelyarit, Text: "В рамках"}, {Cat: CatKancelyarit, Text: "данный"},
	}}
	if r.Verdict() != "потоп" {
		t.Errorf("вердикт %q", r.Verdict())
	}
	top := r.Top(CatKancelyarit, 5)
	if len(top) != 2 || top[0] != "в рамках ×2" || top[1] != "данный" {
		t.Errorf("Top = %v", top)
	}
}

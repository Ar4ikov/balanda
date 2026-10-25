package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/Ar4ikov/balanda/internal/voda"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

func newNalitCmd() *cobra.Command {
	var (
		abzacy int
		stil   string
		zerno  uint64
		width  int
	)
	c := &cobra.Command{
		Use:     "nalit [флаги]",
		Aliases: []string{"pour"},
		GroupID: groupVoda,
		Short:   "налить канцелярской воды",
		Long: `Генерирует связный и бессодержательный русский текст из встроенных
шаблонов. Сеть не нужна. Стили:

  gosuslugi  административный регламент и ответы ведомств
  korporat   письма руководства и синки
  nauka      введения к статьям и авторефератам

Одинаковое --zerno даёт одинаковый текст.`,
		Example: `  balanda nalit
  balanda nalit --abzacy 5 --stil korporat
  balanda nalit -a 2 -s nauka --zerno 7 > vvedenie.txt`,
		Args: exactArgs(0, "ноль аргументов"),
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := voda.GetStil(stil)
			if err != nil {
				return err
			}
			if zerno == 0 {
				zerno = uint64(time.Now().UnixNano())
			}
			ps, err := voda.NewGenerator(s, zerno).Paragraphs(abzacy)
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			if !c.Flags().Changed("shirina") {
				width = 0
				if f, ok := out.(*os.File); ok && vyvod.IsTerminal(f) {
					width = min(vyvod.Width(80), 100)
				}
			}
			for i, p := range ps {
				if i > 0 {
					fmt.Fprintln(out)
				}
				fmt.Fprintln(out, voda.Wrap(p, width))
			}
			return nil
		},
	}
	c.Flags().IntVarP(&abzacy, "abzacy", "a", 3, "сколько `N` абзацев налить")
	c.Flags().StringVarP(&stil, "stil", "s", "gosuslugi", "стиль: `gosuslugi|korporat|nauka`")
	c.Flags().Uint64Var(&zerno, "zerno", 0, "зерно случайности `N`, 0 — каждый раз новое")
	c.Flags().IntVar(&width, "shirina", 0, "переносить строки по ширине `N` (0 — не переносить; в терминале по умолчанию до 100)")
	return c
}

func newProbaCmd() *cobra.Command {
	var (
		short bool
		list  bool
		porog int
	)
	c := &cobra.Command{
		Use:     "proba [флаги] <файл>",
		Aliases: []string{"check"},
		GroupID: groupVoda,
		Short:   "измерить водность текста от 0 до 100",
		Long: `Оценивает, сколько в тексте воды, и подсвечивает находки:

  штампы и пустые обороты  «играет ключевую роль», «стоит отметить»
  канцелярит               «осуществляется», «в рамках», «данный»
  оценки без меры          «эффективный», «значительный»
  офисный жаргон и кальки  «стейкхолдер», «синк», «имеет смысл»
  цепочки отглагольных     «обеспечение повышения эффективности
  существительных          использования»

Ещё в оценку идёт доля отглагольных существительных сверх 4 на 100 слов.
Шкала: до 20 — сухо, до 40 — влажно, до 60 — сыро, до 80 — болото,
дальше потоп. Это эвристика по словарю, а не детектор авторства.

Вместо файла можно передать «-» и подать текст на stdin. В терминале
текст печатается с подсветкой, в трубе — списком находок по строкам.

Словари основаны на каталоге avoid-ai-writing-russian
(github.com/ormeilu/avoid-ai-writing-russian, MIT,
© 2026 Conor Bronsdon, Ilya Lubenets).`,
		Example: `  balanda proba README.md
  balanda nalit | balanda proba -
  balanda proba --porog 40 docs/*.md  # в CI: код 1, если воды больше 40`,
		Args: minArgs(1, "хотя бы один файл (или «-» для stdin)"),
		RunE: func(c *cobra.Command, args []string) error {
			out := c.OutOrStdout()
			p := vyvod.Colors(out)
			over := 0
			for i, name := range args {
				text, err := readInput(name, c.InOrStdin())
				if err != nil {
					return err
				}
				if i > 0 {
					fmt.Fprintln(out)
				}
				if len(args) > 1 {
					fmt.Fprintf(out, "%s== %s ==%s\n", p.Bold, name, p.Reset)
				}
				r := voda.Proba(text)
				if !short {
					if f, ok := out.(*os.File); ok && vyvod.IsTerminal(f) && !list {
						width := min(vyvod.Width(80), 100)
						fmt.Fprintln(out, highlight(p, r, width))
						fmt.Fprintln(out)
						legend(out, p, width)
						fmt.Fprintln(out)
					} else {
						findingsList(out, r)
					}
				}
				probaSummary(out, p, r)
				if porog >= 0 && r.Score > porog {
					over++
				}
			}
			if over > 0 {
				fmt.Fprintf(c.ErrOrStderr(), "balanda: воды больше %d в %d %s\n", porog, over, vyvod.Plural(int64(over), "файле", "файлах", "файлах"))
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&short, "kratko", "k", false, "только оценка и сводка, без текста")
	c.Flags().BoolVarP(&list, "spisok", "l", false, "находки списком по строкам даже в терминале")
	c.Flags().IntVar(&porog, "porog", -1, "вернуть код 1, если водность выше `N`")
	return c
}

func readInput(name string, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if name == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(name)
	}
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New(name + ": не UTF-8, перекодируйте файл")
	}
	return strings.TrimPrefix(string(data), "\uFEFF"), nil
}

func catColor(p vyvod.Palette, c voda.Category) string {
	switch c {
	case voda.CatShtamp:
		return p.Red
	case voda.CatKancelyarit:
		return p.Yellow
	case voda.CatOcenka:
		return p.Magenta
	case voda.CatZhargon:
		return p.Blue
	case voda.CatCepochka:
		return p.Cyan
	}
	return ""
}

var categories = []voda.Category{voda.CatShtamp, voda.CatKancelyarit, voda.CatOcenka, voda.CatZhargon, voda.CatCepochka}

// highlight печатает текст с подсветкой находок и переносит строки
// по словам, чтобы терминал не резал слова посередине.
func highlight(p vyvod.Palette, r *voda.Report, width int) string {
	// paint красит кусок текста [start, end) по находкам.
	paint := func(start, end int) string {
		var b strings.Builder
		pos := start
		for _, f := range r.Findings {
			if f.End <= pos || f.Start >= end {
				continue
			}
			from, to := max(f.Start, pos), min(f.End, end)
			b.WriteString(r.Text[pos:from])
			b.WriteString(catColor(p, f.Cat) + p.Underline + r.Text[from:to] + p.Reset)
			pos = to
		}
		b.WriteString(r.Text[pos:end])
		return b.String()
	}
	var b strings.Builder
	offset := 0
	for i, line := range strings.Split(strings.TrimRight(r.Text, "\n"), "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		col := 0
		for _, w := range wordSpans(line) {
			n := utf8.RuneCountInString(line[w[0]:w[1]])
			if col > 0 && width > 0 && col+1+n > width {
				b.WriteByte('\n')
				col = 0
			} else if col > 0 {
				b.WriteByte(' ')
				col++
			}
			b.WriteString(paint(offset+w[0], offset+w[1]))
			col += n
		}
		offset += len(line) + 1
	}
	return b.String()
}

// wordSpans — границы слов (кусков без пробелов) в строке.
func wordSpans(line string) [][2]int {
	var spans [][2]int
	start := -1
	for i, r := range line {
		if r == ' ' || r == '\t' || r == '\r' {
			if start >= 0 {
				spans = append(spans, [2]int{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		spans = append(spans, [2]int{start, len(line)})
	}
	return spans
}

func legend(out io.Writer, p vyvod.Palette, width int) {
	col := 0
	for _, c := range categories {
		n := utf8.RuneCountInString(c.Title())
		if col > 0 && col+2+n > width {
			fmt.Fprintln(out)
			col = 0
		} else if col > 0 {
			fmt.Fprint(out, "  ")
			col += 2
		}
		fmt.Fprint(out, catColor(p, c)+p.Underline+c.Title()+p.Reset)
		col += n
	}
	fmt.Fprintln(out)
}

func findingsList(out io.Writer, r *voda.Report) {
	for _, f := range r.Findings {
		fmt.Fprintf(out, "строка %d: «%s» — %s\n", f.Line, strings.Join(strings.Fields(f.Text), " "), f.Cat.Title())
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(out)
	}
}

func probaSummary(out io.Writer, p vyvod.Palette, r *voda.Report) {
	color := p.Green
	switch {
	case r.Score >= 60:
		color = p.Red
	case r.Score >= 20:
		color = p.Yellow
	}
	fmt.Fprintf(out, "%sВодность: %s%d из 100%s%s — %s%s\n", p.Bold, color, r.Score, p.Reset, p.Bold, r.Verdict(), p.Reset)
	t := vyvod.Table{Right: map[int]bool{1: true}}
	for _, c := range categories {
		n := r.Counts[c]
		top := ""
		if n > 0 {
			top = strings.Join(r.Top(c, 4), ", ")
		}
		t.Rows = append(t.Rows, []string{"  " + c.Title(), strconv.Itoa(n), p.Dim + top + p.Reset})
	}
	t.Write(out, p)
	per := strings.Replace(strconv.FormatFloat(r.VerbalPer100(), 'f', 1, 64), ".", ",", 1)
	fmt.Fprintf(out, "  отглагольных существительных на 100 слов: %s (в оценку идёт всё сверх 4)\n", per)
	note := ""
	if r.Words > 0 && r.Words < 30 {
		note = " Текст короткий, оценка грубая."
	}
	fmt.Fprintf(out, "%sСлов: %d.%s%s\n", p.Dim, r.Words, note, p.Reset)
}

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
						fmt.Fprintln(out, highlight(p, r))
						fmt.Fprintln(out)
						legend(out, p)
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

func highlight(p vyvod.Palette, r *voda.Report) string {
	var b strings.Builder
	pos := 0
	for _, f := range r.Findings {
		if f.Start < pos {
			continue
		}
		b.WriteString(r.Text[pos:f.Start])
		b.WriteString(catColor(p, f.Cat) + p.Underline + r.Text[f.Start:f.End] + p.Reset)
		pos = f.End
	}
	b.WriteString(r.Text[pos:])
	return strings.TrimRight(b.String(), "\n")
}

func legend(out io.Writer, p vyvod.Palette) {
	var parts []string
	for _, c := range categories {
		parts = append(parts, catColor(p, c)+p.Underline+c.Title()+p.Reset)
	}
	fmt.Fprintln(out, strings.Join(parts, "  "))
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

package cli

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ar4ikov/balanda/internal/baiki"
	"github.com/Ar4ikov/balanda/internal/voda"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

func newTravitCmd() *cobra.Command {
	var (
		tema  string
		list  bool
		zerno uint64
	)
	c := &cobra.Command{
		Use:     "travit [флаги]",
		Aliases: []string{"tell"},
		GroupID: groupBaiki,
		Short:   "рассказать айтишную байку",
		Long: `Печатает случайную байку из встроенного корпуса. Сеть не нужна.
Темы показывает --spisok.

Чтобы байка встречала при открытии терминала, добавьте в ~/.bashrc
или ~/.zshrc строку:

  command -v balanda >/dev/null && balanda travit

В PowerShell — в $PROFILE:

  if (Get-Command balanda -ErrorAction SilentlyContinue) { balanda travit }`,
		Example: `  balanda travit
  balanda travit --tema deploy
  balanda travit --spisok`,
		Args: exactArgs(0, "ноль аргументов"),
		RunE: func(c *cobra.Command, _ []string) error {
			out := c.OutOrStdout()
			p := vyvod.Colors(out)
			if list {
				temy, err := baiki.Temy()
				if err != nil {
					return err
				}
				t := vyvod.Table{Header: []string{"ТЕМА", "НАЗВАНИЕ", "БАЕК"}, Right: map[int]bool{2: true}}
				for _, tm := range temy {
					t.Rows = append(t.Rows, []string{tm.ID, tm.Title, strconv.Itoa(len(tm.Baiki))})
				}
				t.Write(out, p)
				return nil
			}
			if zerno == 0 {
				zerno = uint64(time.Now().UnixNano())
			}
			b, err := baiki.Rasskazat(tema, rand.New(rand.NewPCG(zerno, zerno>>1|1)))
			if err != nil {
				return err
			}
			width := 0
			if f, ok := out.(*os.File); ok && vyvod.IsTerminal(f) {
				width = min(vyvod.Width(80), 80)
			}
			fmt.Fprintln(out, voda.Wrap(b.Text, width))
			fmt.Fprintf(out, "%s— байка про %s, ещё: balanda travit --tema %s%s\n", p.Dim, b.Title, b.Tema, p.Reset)
			return nil
		},
	}
	c.Flags().StringVarP(&tema, "tema", "t", "", "тема: `deploy|git|prod|review|legacy|db|oncall|deadline`")
	c.Flags().BoolVar(&list, "spisok", false, "показать темы")
	c.Flags().Uint64Var(&zerno, "zerno", 0, "зерно случайности `N`, 0 — каждый раз новое")
	return c
}

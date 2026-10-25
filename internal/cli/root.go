// Package cli собирает команды balanda на cobra.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Ar4ikov/balanda/internal/buildinfo"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

const (
	groupKamera = "kamera"
	groupObedki = "obedki"
	groupVoda   = "voda"
	groupBaiki  = "baiki"
)

// ExitError несёт код выхода, который нужно вернуть из процесса.
// Сообщение, если оно есть, уже напечатано.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("код выхода %d", e.Code) }

// NewRoot собирает дерево команд.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "balanda [команда]",
		Short: "песочница, уборка, вода и байки в одном бинарнике",
		Long: `balanda — консольная тулза из четырёх частей:

  камера    запустить процесс с ограниченной пайкой ресурсов и проверить его
  объедки   найти и удалить мусор сборок и кэшей
  вода      налить канцелярской воды или измерить водность текста
  байки     рассказать айтишную байку`,
		Version:               buildinfo.Short(),
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		DisableSuggestions:    true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddGroup(
		&cobra.Group{ID: groupKamera, Title: "Камера (песочница):"},
		&cobra.Group{ID: groupObedki, Title: "Объедки (уборка):"},
		&cobra.Group{ID: groupVoda, Title: "Вода (текст):"},
		&cobra.Group{ID: groupBaiki, Title: "Байки:"},
	)
	root.SetHelpCommandGroupID("")
	root.SetCompletionCommandGroupID("")

	root.AddCommand(
		newSaditCmd(),
		newPereklichkaCmd(),
		newShmonCmd(),
		newOtboyCmd(),
		newScanCmd(),
		newSvaritCmd(),
		newNalitCmd(),
		newProbaCmd(),
		newTravitCmd(),
		newVersionCmd(),
	)
	root.SetHelpCommand(newHelpCmd())

	root.SetVersionTemplate(`balanda {{.Version}}` + "\n")
	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(helpTemplate)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fmt.Errorf("%s\nсправка: %s --help", translateFlagError(err), c.CommandPath())
	})
	cobra.AddTemplateFunc("flagsRu", flagUsages)

	localize(root)
	return root
}

// localize переводит служебные флаги cobra и выключает «[flags]» в строке
// использования: её мы пишем сами.
func localize(c *cobra.Command) {
	c.DisableFlagsInUseLine = true
	c.InitDefaultHelpFlag()
	if f := c.Flags().Lookup("help"); f != nil {
		f.Usage = "показать эту справку"
	}
	if c.Version != "" {
		c.InitDefaultVersionFlag()
		if f := c.Flags().Lookup("version"); f != nil {
			f.Usage = "показать версию"
		}
	}
	for _, sub := range c.Commands() {
		localize(sub)
	}
}

func newHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [команда]",
		Short: "справка по команде",
		Run: func(c *cobra.Command, args []string) {
			target, _, err := c.Root().Find(args)
			if target == nil || err != nil {
				fmt.Fprintf(c.ErrOrStderr(), "нет такой команды: %s\n", strings.Join(args, " "))
				_ = c.Root().Usage()
				return
			}
			_ = target.Help()
		},
	}
}

// Execute запускает CLI и возвращает код выхода.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := NewRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	fmt.Fprintln(stderr, "balanda: "+translateCobraError(root, err))
	return 1
}

var (
	reUnknownCmd = regexp.MustCompile(`^unknown command "([^"]*)" for "([^"]*)"`)
	reArgs       = regexp.MustCompile(`^accepts (\d+) arg\(s\), received (\d+)`)
)

func translateCobraError(root *cobra.Command, err error) string {
	msg := err.Error()
	if m := reUnknownCmd.FindStringSubmatch(msg); m != nil {
		s := fmt.Sprintf("нет такой команды: %s", m[1])
		if sug := root.SuggestionsFor(m[1]); len(sug) > 0 {
			s += "\nможет быть, вы имели в виду: " + strings.Join(sug, ", ")
		}
		return s + "\nсписок команд: balanda --help"
	}
	if m := reArgs.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("нужно аргументов: %s, получено: %s", m[1], m[2])
	}
	return msg
}

func translateFlagError(err error) string {
	msg := err.Error()
	repl := []struct{ from, to string }{
		{"unknown shorthand flag: ", "нет такого короткого флага: "},
		{"unknown flag: ", "нет такого флага: "},
		{"flag needs an argument: ", "флагу нужно значение: "},
		{"bad flag syntax: ", "флаг записан неверно: "},
	}
	for _, r := range repl {
		if strings.HasPrefix(msg, r.from) {
			return r.to + strings.TrimPrefix(msg, r.from)
		}
	}
	if strings.HasPrefix(msg, "invalid argument ") {
		return "неверное значение флага: " + strings.TrimPrefix(msg, "invalid argument ")
	}
	return msg
}

// flagUsages печатает флаги по-русски: с «по умолчанию» вместо «default».
func flagUsages(fs *pflag.FlagSet) string {
	type row struct{ left, usage string }
	var rows []row
	width := 0
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		name, usage := pflag.UnquoteUsage(f)
		left := "      --" + f.Name
		if f.Shorthand != "" {
			left = "  -" + f.Shorthand + ", --" + f.Name
		}
		if name != "" && f.Value.Type() != "bool" {
			left += " " + name
		}
		if def := f.DefValue; def != "" && def != "false" && def != "0" && def != "[]" && f.Name != "help" {
			if f.Value.Type() == "string" {
				def = `"` + def + `"`
			}
			usage += " (по умолчанию " + def + ")"
		}
		if l := len([]rune(left)); l > width {
			width = l
		}
		rows = append(rows, row{left, usage})
	})
	var b strings.Builder
	for _, r := range rows {
		pad := strings.Repeat(" ", width-len([]rune(r.left))+3)
		b.WriteString(r.left + pad + strings.ReplaceAll(r.usage, "\n", "\n"+strings.Repeat(" ", width+3)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

const usageTemplate = `Использование:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <команда> [флаги]{{end}}{{if gt (len .Aliases) 0}}

Другие имена:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Примеры:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Команды:{{range $cmds}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }}  {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) .IsAvailableCommand)}}
  {{rpad .Name .NamePadding }}  {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Прочее:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }}  {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Флаги:
{{flagsRu .LocalFlags}}{{end}}{{if .HasAvailableInheritedFlags}}

Общие флаги:
{{flagsRu .InheritedFlags}}{{end}}{{if .HasAvailableSubCommands}}

Справка по команде: {{.CommandPath}} <команда> --help{{end}}
`

// exactArgs и подобные — проверки аргументов с русскими сообщениями.
func exactArgs(n int, what string) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("%s: нужно %s, получено аргументов: %d\nсправка: %s --help", c.Name(), what, len(args), c.CommandPath())
		}
		return nil
	}
}

func maxArgs(n int, what string) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("%s: можно указать не больше чем %s, получено аргументов: %d\nсправка: %s --help", c.Name(), what, len(args), c.CommandPath())
		}
		return nil
	}
}

func minArgs(n int, what string) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("%s: нужна %s\nсправка: %s --help", c.Name(), what, c.CommandPath())
		}
		return nil
	}
}

// stdinIsTerminal нужен, чтобы не ждать ответа, когда вход не из терминала.
func stdinIsTerminal() bool {
	return vyvod.IsTerminal(os.Stdin)
}

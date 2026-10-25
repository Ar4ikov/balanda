package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Ar4ikov/balanda/internal/kamera"
	"github.com/Ar4ikov/balanda/internal/vyvod"
)

const platformTable = `Как ставятся лимиты:

  Linux, cgroups v2  cpu — доля ядер (cpu.max), mem — memory.max без свопа,
                     time — надзиратель. Root не нужен, но нужна cgroup,
                     делегированная пользователю (обычно systemd user@.service),
                     и процесс balanda должен сидеть внутри неё.
  Linux, rlimit      если cgroups недоступны. mem — RLIMIT_AS на каждый процесс
                     (node, JVM и Go-программы резервируют много адресного
                     пространства и могут не запуститься), cpu — только вместе
                     с time: RLIMIT_CPU = cpu × time секунд процессорного времени.
  macOS              rlimit и сторож: cpu — только вместе с time, бюджет
                     cpu × time секунд процессорного времени; mem — сторож раз
                     в 0,5 с суммирует RSS и время CPU группы и гасит камеру.
  Windows            Job Object: cpu — жёсткий потолок CPU rate, mem — лимит
                     памяти на всё задание, time — надзиратель.

Везде камера — это группа процессов (на Windows — Job Object): по time,
отбою и выходу главного процесса гасится всё дерево. Файловая система
и сеть не изолируются: это ограничитель ресурсов, а не защита
от недоверенного кода.

Какой движок достанется новой камере, пишет balanda pereklichka.`

func newSaditCmd() *cobra.Command {
	var (
		paikaStr string
		name     string
		bg       bool
		quiet    bool
	)
	c := &cobra.Command{
		Use:     "sadit [флаги] <команда> [аргументы]",
		Aliases: []string{"run"},
		GroupID: groupKamera,
		Short:   "посадить процесс в камеру с пайкой ресурсов",
		Long: `Запускает команду в «камере»: с лимитами CPU, памяти и времени.
Без --fon ждёт, пока команда выйдет, и возвращает её код выхода.
Если вышло время, код выхода 124, как у timeout.

Пайка: cpu=<ядра>,mem=<размер>,time=<длительность>
  cpu   число ядер: 1, 0.5, 2
  mem   размер с суффиксом k, m, g: 256m, 1g
  time  30s, 5m, 1h30m или число секунд

` + platformTable,
		Example: `  balanda sadit --paika cpu=1,mem=256m,time=30s -- make test
  balanda sadit --paika time=5s sleep 60
  balanda sadit --fon --imya sborka --paika mem=1g -- go build ./...`,
		Args: minArgs(1, "команда, которую посадить"),
		RunE: func(c *cobra.Command, args []string) error {
			p, err := kamera.ParsePaika(paikaStr)
			if err != nil {
				return err
			}
			s, err := kamera.OpenStore()
			if err != nil {
				return err
			}
			errOut := c.ErrOrStderr()
			notify := errOut
			if quiet {
				notify = io.Discard
			}
			k, code, err := kamera.Sadit(s, kamera.RunOptions{
				Name:       name,
				Cmd:        args,
				Paika:      p,
				Background: bg,
				Stdin:      os.Stdin,
				Stdout:     os.Stdout,
				Stderr:     os.Stderr,
				Notify:     notify,
			})
			if err != nil {
				return err
			}
			if bg {
				out := c.OutOrStdout()
				fmt.Fprintf(out, "%s: сидит, PID %d, движок: %s\n", k.Label(), k.PID, kamera.EngineTitle(k.Engine))
				for _, w := range k.Warnings {
					fmt.Fprintf(errOut, "balanda: %s\n", w)
				}
				fmt.Fprintf(out, "вывод: %s\nаудит: balanda shmon %d\n", k.Stdout, k.ID)
				return nil
			}
			if !quiet {
				if cur, err := s.Load(k.ID); err == nil {
					k = cur
				}
				fmt.Fprintln(errOut, summary(vyvod.Colors(errOut), k))
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
	c.Flags().SetInterspersed(false)
	c.Flags().StringVarP(&paikaStr, "paika", "p", "", "лимиты: `cpu=1,mem=256m,time=30s`")
	c.Flags().StringVarP(&name, "imya", "n", "", "имя камеры для pereklichka и shmon")
	c.Flags().BoolVarP(&bg, "fon", "d", false, "запустить в фоне, вывод писать в журнал камеры")
	c.Flags().BoolVarP(&quiet, "tiho", "q", false, "не писать итог и предупреждения в stderr")
	return c
}

func summary(p vyvod.Palette, k *kamera.Kamera) string {
	var b strings.Builder
	b.WriteString(p.Dim + "balanda: " + k.Label() + " " + statusText(k))
	b.WriteString(", " + vyvod.Duration(k.Elapsed(time.Now())))
	if u := k.Usage; u != nil {
		b.WriteString(", CPU " + vyvod.Duration(time.Duration(u.UserMs+u.SysMs)*time.Millisecond))
		if u.PeakMem > 0 {
			b.WriteString(", пик памяти " + vyvod.Size(u.PeakMem))
		}
	}
	if k.Engine != "" {
		b.WriteString(", движок: " + kamera.EngineTitle(k.Engine))
	}
	return b.String() + p.Reset
}

func statusText(k *kamera.Kamera) string {
	switch k.Status {
	case kamera.StatusVyshla:
		if k.Error != "" {
			return "не запустилась"
		}
		if k.ExitCode != nil {
			return "вышла, код " + strconv.Itoa(*k.ExitCode)
		}
		return "вышла"
	case kamera.StatusUbita:
		return "убита: " + kamera.ReasonTitle(k.Reason)
	}
	return k.Status.Title()
}

func colorStatus(p vyvod.Palette, k *kamera.Kamera, procAlive, nadzorAlive bool) string {
	switch k.Status {
	case kamera.StatusSidit:
		if k.PID != 0 && !nadzorAlive && procAlive {
			return p.Yellow + "сидит без надзора" + p.Reset
		}
		return p.Green + "сидит" + p.Reset
	case kamera.StatusVyshla:
		if k.ExitCode != nil && *k.ExitCode == 0 {
			return statusText(k)
		}
		return p.Yellow + statusText(k) + p.Reset
	case kamera.StatusUbita:
		return p.Red + statusText(k) + p.Reset
	case kamera.StatusPoteryana:
		return p.Yellow + statusText(k) + p.Reset
	}
	return statusText(k)
}

func newPereklichkaCmd() *cobra.Command {
	var (
		onlyAlive bool
		forget    bool
	)
	c := &cobra.Command{
		Use:     "pereklichka [флаги]",
		Aliases: []string{"ps"},
		GroupID: groupKamera,
		Short:   "список камер и их состояние",
		Long: `Показывает все камеры и проверяет каждую сидящую: жив ли процесс
и жив ли его надзиратель. Камера, у которой пропали оба, получает
статус «потеряна».

Записи хранятся в каталоге состояния: $XDG_STATE_HOME/balanda
(по умолчанию ~/.local/state/balanda) на Linux, в os.UserConfigDir()
на macOS и Windows. Каталог можно задать через BALANDA_STATE_DIR.`,
		Args: exactArgs(0, "ноль аргументов"),
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := kamera.OpenStore()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			p := vyvod.Colors(out)
			all, err := s.List()
			if err != nil {
				return err
			}
			eng, info := kamera.EngineFor()
			fmt.Fprintf(out, "%sновые камеры с пайкой: %s (%s)%s\n", p.Dim, kamera.EngineTitle(eng), info, p.Reset)
			if forget {
				n := 0
				for _, k := range all {
					kamera.Check(s, k)
					if k.Status != kamera.StatusSidit {
						if err := s.Remove(k); err == nil {
							n++
						}
					}
				}
				fmt.Fprintf(out, "забыто записей: %d\n", n)
				return nil
			}
			t := vyvod.Table{
				Header: []string{"№", "ИМЯ", "СТАТУС", "ПАЙКА", "ПРОШЛО", "ПАМЯТЬ", "PID", "КОМАНДА"},
				Right:  map[int]bool{0: true, 4: true, 5: true, 6: true},
			}
			now := time.Now()
			width := vyvod.Width(100)
			for _, k := range all {
				procAlive, nadzorAlive := kamera.Check(s, k)
				if onlyAlive && k.Status != kamera.StatusSidit {
					continue
				}
				mem, pid := "—", "—"
				if k.Status == kamera.StatusSidit {
					if procAlive {
						pid = strconv.Itoa(k.PID)
					}
					if st, err := kamera.LiveStats(k); err == nil && st.Mem >= 0 {
						mem = vyvod.Size(st.Mem)
					}
				} else if k.Usage != nil && k.Usage.PeakMem > 0 {
					mem = vyvod.Size(k.Usage.PeakMem)
				}
				t.Rows = append(t.Rows, []string{
					strconv.Itoa(k.ID), k.Name, colorStatus(p, k, procAlive, nadzorAlive),
					k.Paika.String(), vyvod.Duration(k.Elapsed(now)), mem, pid,
					vyvod.Truncate(k.CmdLine(), max(20, width-80)),
				})
			}
			if len(t.Rows) == 0 {
				if onlyAlive {
					fmt.Fprintln(out, "Сидящих камер нет.")
				} else {
					fmt.Fprintln(out, "Камер нет. Посадить: balanda sadit -- <команда>")
				}
				return nil
			}
			t.Write(out, p)
			return nil
		},
	}
	c.Flags().BoolVar(&onlyAlive, "zhivye", false, "только сидящие камеры")
	c.Flags().BoolVar(&forget, "zabyt", false, "удалить записи о камерах, которые уже не сидят")
	return c
}

func newShmonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "shmon <камера>",
		Aliases: []string{"audit"},
		GroupID: groupKamera,
		Short:   "аудит камеры: CPU, память, файлы, процессы, код выхода",
		Long: `Показывает всё, что известно о камере. Камеру можно указать номером
или именем.

У сидящей камеры данные живые: на Linux из /proc и cgroup, на macOS
из ps и lsof, на Windows из Job Object (список открытых файлов
Windows без отладочных прав не отдаёт, вместо него число дескрипторов).
У вышедшей камеры — итог, который записал надзиратель.`,
		Example: `  balanda shmon 3
  balanda shmon sborka`,
		Args: exactArgs(1, "одна камера (номер или имя)"),
		RunE: func(c *cobra.Command, args []string) error {
			s, err := kamera.OpenStore()
			if err != nil {
				return err
			}
			k, err := s.Find(args[0])
			if err != nil {
				return err
			}
			procAlive, nadzorAlive := kamera.Check(s, k)
			out := c.OutOrStdout()
			p := vyvod.Colors(out)
			row := func(label, val string) {
				fmt.Fprintf(out, "%s%-16s%s %s\n", p.Dim, label, p.Reset, val)
			}
			fmt.Fprintf(out, "%s%s%s — %s, %s\n", p.Bold, k.Label(), p.Reset, colorStatus(p, k, procAlive, nadzorAlive), vyvod.Duration(k.Elapsed(time.Now())))
			row("команда", k.CmdLine())
			row("каталог", k.Dir)
			row("пайка", k.Paika.String())
			eng := kamera.EngineTitle(k.Engine)
			if k.EngineInfo != "" {
				eng += " (" + k.EngineInfo + ")"
			}
			row("движок", eng)
			row("посажена", k.Started.Format("2006-01-02 15:04:05"))
			if k.PID != 0 {
				row("PID", fmt.Sprintf("%d, надзиратель %d", k.PID, k.Nadzor))
			}
			for _, w := range k.Warnings {
				row("внимание", w)
			}
			if k.Error != "" {
				row("ошибка", k.Error)
			}
			if k.Background {
				row("stdout", k.Stdout)
				row("stderr", k.Stderr)
			}
			if k.Status == kamera.StatusSidit {
				st, err := kamera.LiveStats(k)
				if err != nil {
					row("данные", "нет: "+err.Error())
					return nil
				}
				row("источник", st.Source)
				cpu := vyvod.Duration(st.CPUTotal)
				if st.CPUUser+st.CPUSys > 0 {
					cpu += fmt.Sprintf(" (user %s, sys %s)", vyvod.Duration(st.CPUUser), vyvod.Duration(st.CPUSys))
				}
				row("CPU", cpu)
				mem := vyvod.Size(st.Mem)
				if st.PeakMem > 0 {
					mem += ", пик " + vyvod.Size(st.PeakMem)
				}
				if k.Paika.Mem > 0 {
					mem += " из " + vyvod.Size(k.Paika.Mem)
				}
				row("память", mem)
				row("процессы", strconv.Itoa(len(st.Procs)))
				for _, pr := range st.Procs {
					m := ""
					if pr.Mem > 0 {
						m = "  " + vyvod.Size(pr.Mem)
					}
					fmt.Fprintf(out, "  %7d  %s%s%s%s\n", pr.PID, vyvod.Truncate(pr.Cmd, 70), p.Dim, m, p.Reset)
				}
				switch {
				case st.FilesNote != "" && st.FilesCount == 0:
					row("открытые файлы", st.FilesNote)
				default:
					row("открытые файлы", strconv.Itoa(st.FilesCount))
					for _, f := range st.Files {
						fmt.Fprintf(out, "  %s\n", f)
					}
					if st.FilesCount > len(st.Files) {
						fmt.Fprintf(out, "  %s… и ещё %d%s\n", p.Dim, st.FilesCount-len(st.Files), p.Reset)
					}
				}
				row("код выхода", "— (ещё сидит)")
				return nil
			}
			if u := k.Usage; u != nil {
				row("CPU", fmt.Sprintf("%s (user %s, sys %s)",
					vyvod.Duration(time.Duration(u.UserMs+u.SysMs)*time.Millisecond),
					vyvod.Duration(time.Duration(u.UserMs)*time.Millisecond),
					vyvod.Duration(time.Duration(u.SysMs)*time.Millisecond)))
				if u.PeakMem > 0 {
					row("пик памяти", vyvod.Size(u.PeakMem))
				}
			}
			code := "неизвестен"
			if k.ExitCode != nil {
				code = strconv.Itoa(*k.ExitCode)
			}
			if k.Signal != "" {
				code += ", сигнал " + k.Signal
			}
			if k.Reason != "" {
				code += ", причина: " + kamera.ReasonTitle(k.Reason)
			}
			row("код выхода", code)
			return nil
		},
	}
	return c
}

func newOtboyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "otboy [камера...]",
		Aliases: []string{"stop"},
		GroupID: groupKamera,
		Short:   "погасить все камеры или только указанные",
		Long: `Гасит сидящие камеры: всё дерево процессов получает SIGTERM,
через 2 секунды SIGKILL (на Windows — TerminateJobObject сразу).
Без аргументов гасит все сидящие камеры.`,
		Example: `  balanda otboy
  balanda otboy 3 sborka`,
		RunE: func(c *cobra.Command, args []string) error {
			s, err := kamera.OpenStore()
			if err != nil {
				return err
			}
			var targets []*kamera.Kamera
			if len(args) == 0 {
				all, err := s.List()
				if err != nil {
					return err
				}
				for _, k := range all {
					kamera.Check(s, k)
					if k.Status == kamera.StatusSidit {
						targets = append(targets, k)
					}
				}
			} else {
				for _, a := range args {
					k, err := s.Find(a)
					if err != nil {
						return err
					}
					targets = append(targets, k)
				}
			}
			out := c.OutOrStdout()
			if len(targets) == 0 {
				fmt.Fprintln(out, "Сидящих камер нет, гасить некого.")
				return nil
			}
			failed := 0
			for _, k := range targets {
				if k.Status != kamera.StatusSidit {
					fmt.Fprintf(out, "%s: уже %s\n", k.Label(), statusText(k))
					continue
				}
				if err := kamera.Otboy(s, k); err != nil {
					failed++
					fmt.Fprintf(c.ErrOrStderr(), "balanda: %s: %v\n", k.Label(), err)
					continue
				}
				fmt.Fprintf(out, "%s: %s\n", k.Label(), statusText(k))
			}
			if failed > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	return c
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Ar4ikov/balanda/internal/buildinfo"
)

func newVersionCmd() *cobra.Command {
	var short bool
	c := &cobra.Command{
		Use:   "version [флаги]",
		Short: "версия, коммит и дата сборки",
		Args:  exactArgs(0, "ноль аргументов"),
		RunE: func(c *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			out := c.OutOrStdout()
			if short {
				fmt.Fprintln(out, info.Version)
				return nil
			}
			fmt.Fprintf(out, "balanda %s\n", info.Version)
			fmt.Fprintf(out, "коммит:    %s\n", info.Commit)
			fmt.Fprintf(out, "собрано:   %s\n", info.Date)
			fmt.Fprintf(out, "go:        %s\n", info.GoVersion)
			fmt.Fprintf(out, "платформа: %s\n", info.Platform)
			return nil
		},
	}
	c.Flags().BoolVarP(&short, "short", "s", false, "только номер версии")
	return c
}

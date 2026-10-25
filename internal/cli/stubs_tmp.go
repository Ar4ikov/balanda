package cli

import "github.com/spf13/cobra"

func newTravitCmd() *cobra.Command { return &cobra.Command{Use: "travit", GroupID: groupBaiki} }

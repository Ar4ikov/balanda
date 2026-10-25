package cli

import "github.com/spf13/cobra"

func newNalitCmd() *cobra.Command  { return &cobra.Command{Use: "nalit", GroupID: groupVoda} }
func newProbaCmd() *cobra.Command  { return &cobra.Command{Use: "proba", GroupID: groupVoda} }
func newTravitCmd() *cobra.Command { return &cobra.Command{Use: "travit", GroupID: groupBaiki} }

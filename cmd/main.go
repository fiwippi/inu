package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"inu/fs"
)

var inu = fs.NewFS("inu.db")

var rootCmd = &cobra.Command{
	Use:               "inu",
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
}

var addCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Add a path to the DAG",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, rs, err := inu.AddPath(args[0])
		if err != nil {
			return err
		}

		for _, r := range rs {
			fmt.Printf("%s %s\n", r.CID, r.Path)
		}
		return nil
	},
}

var catCmd = &cobra.Command{
	Use:   "cat [path]",
	Short: "Read a path from the DAG",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := inu.ResolvePath(args[0])
		if err != nil {
			return err
		}

		data, err := inu.ReadBytes(&n)
		if err != nil {
			return err
		}

		_, err = os.Stdout.Write(data)
		return err
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(catCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

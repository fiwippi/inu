package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const storePath = "inu.db"

var rootCmd = &cobra.Command{
	Use:               "inu",
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
}

func init() {
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(dhtCmd)
	rootCmd.AddCommand(uploadCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"inu/fs"
)

var addCmd = &cobra.Command{
	Use:   "add path",
	Short: "Add a path to the DAG store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, rs, err := fs.NewFS(storePath).AddPath(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("added %d item(s)\n", len(rs))
		for _, r := range rs {
			fmt.Printf("%s %s\n", r.CID, r.Path)
		}
		return nil
	},
}

package main

import (
	"crypto/rand"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"inu/dht"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Generate a DHT key",
}

var keyRandCmd = &cobra.Command{
	Use:   "rand",
	Short: "Generate a random key",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		k := dht.Key{}
		if _, err := rand.Read(k[:]); err != nil {
			return err
		}

		fmt.Println(k.MarshalB32())
		return nil
	},
}

var keyUintCmd = &cobra.Command{
	Use:   "uint n",
	Short: "Generate a key from a uint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}

		fmt.Println(dht.ParseUint64(n).MarshalB32())
		return nil
	},
}

func init() {
	keyCmd.AddCommand(keyRandCmd)
	keyCmd.AddCommand(keyUintCmd)
}

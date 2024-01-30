package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"inu"
	"inu/dht"
	"inu/merkle"
	"inu/store"
)

var uploadCmd = &cobra.Command{
	Use:   "upload CID",
	Short: "Upload a Merkle DAG node to the DHT",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse store and client config
		storePath, err := cmd.Flags().GetString("store")
		if err != nil {
			return err
		}
		configPath, err := cmd.Flags().GetString("file")
		if err != nil {
			return err
		}

		c := dht.DefaultClientConfig()
		if configPath != "" {
			data, err := os.ReadFile(configPath)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &c); err != nil {
				return err
			}
		}

		// Setup store and client
		s := store.NewStore(storePath)
		defer s.Close()
		client := dht.NewClient(c)

		// Get all CIDs of the root CID and
		// upload each one to the store
		cids, err := getCIDs(inu.CID(args[0]), s)
		if err != nil {
			return err
		}
		for _, cid := range cids {
			k, err := dht.ParseCID(cid)
			if err != nil {
				return err
			}
			if err := client.PutKey(k); err != nil {
				return err
			}
		}
		fmt.Printf("uploaded %d item(s)\n", len(cids))

		return nil
	},
}

func init() {
	uploadCmd.PersistentFlags().StringP("store", "s", "inu.db", "store path")
	uploadCmd.PersistentFlags().StringP("file", "f", "", "client configuration file")
}

func getCIDs(cid inu.CID, s *store.Store) ([]inu.CID, error) {
	b, err := s.Get(cid)
	if err != nil {
		return nil, err
	}
	m, err := merkle.ParseBlock(b)
	if err != nil {
		return nil, err
	}

	cids := []inu.CID{cid}
	for _, l := range m.Links() {
		cs, err := getCIDs(l.CID, s)
		if err != nil {
			return nil, err
		}
		cids = append(cids, cs...)
	}

	return cids, nil
}

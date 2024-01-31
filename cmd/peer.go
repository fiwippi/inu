package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"inu"
	"inu/dht"
)

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Put or get data from a DHT network",
}

var peerDaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the peer daemon process",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := parsePeerConfig(cmd)
		if err != nil {
			return err
		}
		d := newDaemon(c)

		d.Start()
		<-done()
		return d.Stop()
	},
}

var peerAddCmd = &cobra.Command{
	Use:   "add path",
	Short: "Add a path to the DAG store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := parsePeerConfig(cmd)
		if err != nil {
			return err
		}
		api := newDaemonAPI(c)

		if offline, err := api.offline(); err == nil && offline {
			d := newDaemon(c)
			d.Start()
			defer d.Stop()
		}

		abs, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		rs, err := api.add(abs)
		if err != nil {
			return err
		}

		parent := strings.TrimSuffix(abs, args[0])
		fmt.Printf("added %d item(s)\n", len(rs))
		for _, r := range rs {
			fmt.Printf("%s %s\n", r.CID, strings.TrimPrefix(r.Path, parent))
		}

		return nil
	},
}

var peerUploadCmd = &cobra.Command{
	Use:   "upload CID",
	Short: "Upload a Merkle DAG node to the DHT",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := parsePeerConfig(cmd)
		if err != nil {
			return err
		}
		api := newDaemonAPI(c)

		if offline, err := api.offline(); err == nil && offline {
			d := newDaemon(c)
			d.Start()
			defer d.Stop()
		}

		count, err := api.upload(inu.CID(args[0]))
		if err != nil {
			return err
		}
		fmt.Printf("uploaded %d item(s)\n", count)

		return nil
	},
}

func init() {
	peerCmd.AddCommand(peerDaemonCmd)
	peerCmd.AddCommand(peerAddCmd)
	peerCmd.AddCommand(peerUploadCmd)

	peerCmd.PersistentFlags().StringP("file", "f", "", "peer configuration file")
}

func parsePeerConfig(cmd *cobra.Command) (dht.ClientConfig, error) {
	configPath, err := cmd.Flags().GetString("file")
	if err != nil {
		return dht.ClientConfig{}, err
	}

	c := dht.DefaultClientConfig()
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return dht.ClientConfig{}, err
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return dht.ClientConfig{}, err
		}
	}

	return c, nil
}

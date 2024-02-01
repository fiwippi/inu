package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"inu"
	"inu/cid"
	"inu/dht"
)

const storePath = "inu.db"

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
		d := inu.NewDaemon(storePath, c)

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
		return runApiFunc(cmd, func(api *inu.API) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			rs, err := api.AddPath(abs)
			if err != nil {
				return err
			}

			parent := strings.TrimSuffix(abs, args[0])
			fmt.Printf("added %d item(s)\n", len(rs))
			for _, r := range rs {
				fmt.Printf("%s %s\n", r.CID, strings.TrimPrefix(r.Path, parent))
			}

			return nil
		})
	},
}

var peerCatCmd = &cobra.Command{
	Use:   "cat CID",
	Short: "Print a file represented by a CID on the standard output",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runApiFunc(cmd, func(api *inu.API) error {
			data, err := api.Cat(args[0])
			if err != nil {
				return err
			}

			_, err = os.Stdout.Write(data)
			return err
		})
	},
}

var peerUploadCmd = &cobra.Command{
	Use:   "upload CID",
	Short: "Upload a Merkle DAG to the DHT",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runApiFunc(cmd, func(api *inu.API) error {
			count, err := api.Upload(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("uploaded %d node(s)\n", count)

			return nil
		})
	},
}

var peerDownloadCmd = &cobra.Command{
	Use:   "dl CID",
	Short: "Download a Merkle DAG from the DHT",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runApiFunc(cmd, func(api *inu.API) error {
			if err := api.Download(cid.CID(args[0])); err != nil {
				return err
			}
			fmt.Println("download successful")

			return nil
		})
	},
}

func init() {
	peerCmd.AddCommand(peerDaemonCmd)
	peerCmd.AddCommand(peerAddCmd)
	peerCmd.AddCommand(peerCatCmd)
	peerCmd.AddCommand(peerUploadCmd)
	peerCmd.AddCommand(peerDownloadCmd)

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

func runApiFunc(cmd *cobra.Command, fn func(api *inu.API) error) error {
	c, err := parsePeerConfig(cmd)
	if err != nil {
		return err
	}

	if offline, err := daemonOffline(c.Port + 1000); err == nil && offline {
		d := inu.NewDaemon(storePath, c)
		d.Start()
		defer d.Stop()
		time.Sleep(1 * time.Second)
	}

	api, err := inu.NewAPI(c)
	if err != nil {
		return err
	}

	return fn(api)
}

func daemonOffline(p uint16) (bool, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
	if err == nil {
		if err := ln.Close(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

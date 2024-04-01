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
		c, err := parseDaemonConfig(cmd)
		if err != nil {
			return err
		}

		d := inu.NewDaemon(c)

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

func parseDaemonConfig(cmd *cobra.Command) (inu.DaemonConfig, error) {
	configPath, err := cmd.Flags().GetString("file")
	if err != nil {
		return inu.DaemonConfig{}, err
	}

	c := inu.DefaultDaemonConfig()
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return inu.DaemonConfig{}, err
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return inu.DaemonConfig{}, err
		}
	}

	clientNodes := os.Getenv("INU_DAEMON_DHT_NODES")
	if clientNodes != "" {
		var nodes []string
		if err := json.Unmarshal([]byte(clientNodes), &nodes); err != nil {
			return inu.DaemonConfig{}, err
		}
		c.Nodes = nodes
	}
	storePath := os.Getenv("INU_DAEMON_STORE_PATH")
	if storePath != "" {
		c.StorePath = storePath
	}
	publicIP := os.Getenv("INU_DAEMON_PUBLIC_IP")
	if publicIP != "" {
		c.PublicIP = publicIP
	}

	return c, nil
}

func runApiFunc(cmd *cobra.Command, fn func(api *inu.API) error) error {
	c, err := parseDaemonConfig(cmd)
	if err != nil {
		return err
	}

	if offline, err := daemonOffline(c.RpcPort); err == nil && offline {
		d := inu.NewDaemon(c)
		d.Start()
		defer d.Stop()
		time.Sleep(1 * time.Second)
	}

	api, err := inu.NewAPIFromConfig(c)
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

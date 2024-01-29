package main

import (
	"encoding/csv"
	"encoding/json"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"inu/dht"
)

var dhtCmd = &cobra.Command{
	Use:   "dht",
	Short: "Start or join a DHT network",
}

var dhtStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Run a DHT node in a new network",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := parseNode(cmd)
		if err != nil {
			return err
		}

		n.Start()
		<-done()
		return n.Stop()
	},
}

var dhtJoinCmd = &cobra.Command{
	Use:   "join ID address",
	Short: "Run a DHT node in a pre-existing network",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := parseNode(cmd)
		if err != nil {
			return err
		}

		n.Start()
		k := dht.Key{}
		if err := k.UnmarshalB32(args[0]); err != nil {
			return err
		}
		if err := n.Bootstrap(dht.Contact{ID: k, Address: args[1]}); err != nil {
			return err
		}

		<-done()
		return n.Stop()
	},
}

func init() {
	dhtCmd.AddCommand(dhtStartCmd)
	dhtCmd.AddCommand(dhtJoinCmd)

	dhtCmd.PersistentFlags().StringP("file", "f", "", "node configuration file")
	dhtCmd.PersistentFlags().StringP("asn", "a", "", "asn mapping file")
}

func parseNode(cmd *cobra.Command) (*dht.Node, error) {
	// Load the config file or use the default
	// config if unspecified
	configPath, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, err
	}
	asnPath, err := cmd.Flags().GetString("asn")
	if err != nil {
		return nil, err
	}

	c := dht.DefaultNodeConfig()
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, err
		}
	}

	// Create the node
	n, err := dht.NewNode(c)
	if err != nil {
		return nil, err
	}

	// Add IP to ASN mappings if specified
	if asnPath != "" {
		f, err := os.Open(asnPath)
		if err != nil {
			return nil, err
		}

		r := csv.NewReader(f)
		r.Comma = '\t'
		r.FieldsPerRecord = 2

		ls, err := r.ReadAll()
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			p, err := netip.ParsePrefix(l[0])
			if err != nil {
				return nil, err
			}
			asn, err := strconv.Atoi(l[1])
			if err != nil {
				return nil, err
			}
			if err := n.UpdateASN(p, asn); err != nil {
				return nil, err
			}
		}
	}

	return n, nil
}

func done() <-chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	return c
}

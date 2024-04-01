package dht

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"

	"inu/cert"
)

// Client Config

type ClientConfig struct {
	// Client data
	Port uint16 `json:"port"`

	// DHT data
	Nodes     []string `json:"nodes"`
	UploadKey Key      `json:"upload_key"`
}

func DefaultClientConfig() ClientConfig {
	nConf := DefaultNodeConfig()

	return ClientConfig{
		Port:      6000,
		Nodes:     []string{fmt.Sprintf("%s:%d", nConf.Host, nConf.Port)},
		UploadKey: Key{},
	}
}

// Client

type Client struct {
	client *http.Client

	config ClientConfig
}

func NewClient(config ClientConfig) *Client {
	return &Client{
		config: config,
		client: cert.Client(),
	}
}

func (c *Client) nodeURL(k Key) string {
	addr := c.config.Nodes[rand.Intn(len(c.config.Nodes))]
	return "https://" + addr + "/key/" + k.MarshalB32()
}

func (c *Client) FindPeers(k Key) ([]Peer, error) {
	// Send GET and expect a 200 OK response code
	resp, err := c.client.Get(c.nodeURL(k))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, badCodeError(resp.StatusCode)
	}

	// Decode the peers
	var ps []Peer
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return nil, err
	}

	if len(ps) == 0 {
		panic("should not return 0 peers")
	}

	return ps, nil
}

func (c *Client) PutKey(k Key) error {
	// Encode the PUT
	data, err := json.Marshal(c.config.Port)
	if err != nil {
		return err
	}

	// Create the request
	req, err := http.NewRequest("PUT", c.nodeURL(k), bytes.NewReader(data))
	if err != nil {
		return err
	}
	if c.config.UploadKey != (Key{}) {
		req.Header.Add("Inu-Upload", c.config.UploadKey.MarshalB32())
	}

	// Send PUT and expect a 200 OK response code
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return badCodeError(resp.StatusCode)
	}

	return nil
}

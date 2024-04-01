package dht

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"inu/cert"
)

// Interface

type rpc interface {
	Ping(dst Contact) error
	Store(dst Contact, k Key, p []Peer) error
	FindNode(dst Contact, k Key) ([]Contact, error)
	FindPeers(dst Contact, k Key) (findPeersResp, error)
}

type pair struct {
	K Key    `json:"k"`
	P []Peer `json:"p"`
}

type findPeersResp struct {
	Found    bool      `json:"found"`
	Peers    []Peer    `json:"peers"`
	Contacts []Contact `json:"contacts"`
}

// Client

type rpcClient struct {
	client      *http.Client
	srcHeader   string
	swarmHeader string
}

func newRpc(c Contact, swarmKey Key) rpc {
	h, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}

	return &rpcClient{
		client:      cert.Client(),
		srcHeader:   string(h),
		swarmHeader: swarmKey.MarshalB32(),
	}
}

func (rpc *rpcClient) postJSON(address, endpoint string, v any) (*http.Response, error) {
	// Marshal data
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Create the request
	url := "https://" + address + "/" + endpoint
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Inu-Src", rpc.srcHeader)
	req.Header.Set("Inu-Swarm", rpc.swarmHeader)

	// Send POST and expect a 200 OK response code
	resp, err := rpc.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, badCodeError(resp.StatusCode)
	}
	return resp, nil
}

func (rpc *rpcClient) getJSON(address, endpoint string) (*http.Response, error) {
	// Create the request
	req, err := http.NewRequest("GET", "https://"+address+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Inu-Src", rpc.srcHeader)
	req.Header.Set("Inu-Swarm", rpc.swarmHeader)

	// Send GET and expect a 200 OK response code
	resp, err := rpc.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, badCodeError(resp.StatusCode)
	}

	return resp, nil
}

func (rpc *rpcClient) Ping(dst Contact) error {
	resp, err := rpc.getJSON(dst.Address, "rpc/ping")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (rpc *rpcClient) Store(dst Contact, k Key, p []Peer) error {
	resp, err := rpc.postJSON(dst.Address, "rpc/store", pair{k, p})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (rpc *rpcClient) FindNode(dst Contact, k Key) ([]Contact, error) {
	resp, err := rpc.getJSON(dst.Address, "rpc/find-node/"+k.MarshalB32())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cs []Contact
	if err := json.NewDecoder(resp.Body).Decode(&cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func (rpc *rpcClient) FindPeers(dst Contact, k Key) (findPeersResp, error) {
	resp, err := rpc.getJSON(dst.Address, "rpc/find-peers/"+k.MarshalB32())
	if err != nil {
		return findPeersResp{}, err
	}
	defer resp.Body.Close()

	var v findPeersResp
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return findPeersResp{}, err
	}
	return v, nil
}

func badCodeError(c int) error {
	return fmt.Errorf("bad response code: %d", c)
}

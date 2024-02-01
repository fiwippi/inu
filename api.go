package inu

import (
	"crypto/tls"
	"fmt"
	"net/rpc"

	"inu/cert"
	"inu/cid"
	"inu/dht"
	"inu/fs"
)

type API struct {
	port   uint16
	client *rpc.Client
}

func NewAPI(c dht.ClientConfig) (*API, error) {

	conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port+1000), cert.Config())
	if err != nil {
		return nil, err
	}

	return &API{
		port:   c.Port + 1000,
		client: rpc.NewClient(conn),
	}, nil
}

func (api *API) Close() error {
	if api.client != nil {
		return api.client.Close()
	}
	return nil
}

func (api *API) AddPath(path string) ([]fs.Record, error) {
	r := new(AddReply)
	err := api.client.Call("Daemon.AddPath", path, &r)
	return r.Records, err
}

func (api *API) Cat(path string) ([]byte, error) {
	r := new(CatReply)
	err := api.client.Call("Daemon.Cat", path, &r)
	return r.Data, err
}

func (api *API) Upload(path string) (uint, error) {
	c := new(uint)
	err := api.client.Call("Daemon.Upload", path, &c)
	return *c, err
}

func (api *API) Download(cid cid.CID) error {
	return api.client.Call("Daemon.Download", cid, &struct{}{})
}

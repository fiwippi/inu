package inu

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/rpc"
	"time"

	"inu/cert"
	"inu/cid"
	"inu/fs"
)

type API struct {
	port   uint16
	client *rpc.Client
}

func NewAPI(host string, port uint16) (*API, error) {
	d := tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout:   10 * time.Minute,
			KeepAlive: 10 * time.Minute,
		},
		Config: cert.Config(),
	}
	conn, err := d.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}

	return &API{
		port:   port,
		client: rpc.NewClient(conn),
	}, nil
}

func NewAPIFromConfig(config DaemonConfig) (*API, error) {
	return NewAPI(config.Host, config.RpcPort)
}

func (api *API) Close() error {
	if api.client != nil {
		return api.client.Close()
	}
	return nil
}

func (api *API) AddBytes(bytes []byte) (cid.CID, error) {
	r := new(AddBytesReply)
	err := api.client.Call("Daemon.AddBytes", bytes, &r)
	return r.CID, err
}

func (api *API) AddPath(path string) ([]fs.Record, error) {
	r := new(AddPathReply)
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

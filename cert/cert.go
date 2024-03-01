package cert

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
	"time"
)

var (
	//go:embed cert.pem
	Pem []byte

	//go:embed cert.key
	Key []byte
)

var config *tls.Config

func init() {
	// Create the TLS config
	tlsCert, err := tls.X509KeyPair(Pem, Key)
	if err != nil {
		panic(err)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(Pem)

	config = &tls.Config{
		ServerName:   "localhost",
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      certPool,
	}
}

func Config() *tls.Config {
	return config.Clone()
}

func Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:     Config(),
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: http.DefaultMaxIdleConnsPerHost,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

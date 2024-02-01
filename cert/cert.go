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

func Config() *tls.Config {
	tlsCert, err := tls.X509KeyPair(Pem, Key)
	if err != nil {
		panic(err)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(Pem)

	return &tls.Config{
		ServerName:   "localhost",
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      certPool,
	}
}

func Client(timeout time.Duration) *http.Client {
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(Pem)

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: Config(),
		},
	}
}

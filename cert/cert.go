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

func Cert() tls.Certificate {
	tlsCert, err := tls.X509KeyPair(Pem, Key)
	if err != nil {
		panic(err)
	}
	return tlsCert
}

func Client(timeout time.Duration) *http.Client {
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(Pem)

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName: "localhost",
				RootCAs:    certPool,
			},
		},
	}
}

package cert

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net"
	"net/http"
	"os"
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
		MinVersion:   tls.VersionTLS13,
	}

	keylogFile := os.Getenv("INU_KEYLOG_FILE")
	if keylogFile != "" {
		w, err := os.OpenFile(keylogFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			panic(err)
		}
		config.KeyLogWriter = w
	}
}

func Config() *tls.Config {
	return config.Clone()
}

func Client() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        1024,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig:     Config(),
		},
	}
}

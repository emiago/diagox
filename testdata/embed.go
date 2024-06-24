// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package testdata

import (
	"crypto/tls"
	"crypto/x509"
	"embed"
	"io/fs"
	"path"
)

//go:embed files/*.wav
var filesDir embed.FS

//go:embed configs/*
var configsDir embed.FS

func OpenFile(filename string) (fs.File, error) {
	return filesDir.Open(path.Join("files", filename))
}

func OpenConfigFile(filename string) (fs.File, error) {
	return configsDir.Open(path.Join("configs", filename))
}

// This will generate TLS certificates needed for test below
// openssl is required
//go:generate bash -c "cd testdata && ./generate_certs_rsa.sh"

var (
	//go:embed certs/server.crt
	rootCA []byte

	//go:embed certs/server.crt
	serverCRT []byte

	//go:embed certs/server.key
	serverKEY []byte

	//go:embed certs/client.crt
	clientCRT []byte

	//go:embed certs/client.key
	clientKEY []byte
)

func ServerTLSConfig() *tls.Config {
	cert, err := tls.X509KeyPair(serverCRT, serverKEY)
	if err != nil {
		panic(err)
	}

	cfg := &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{cert},
	}

	return cfg
}

func ClientTLSConfig() *tls.Config {
	cert, err := tls.X509KeyPair(clientCRT, clientKEY)
	if err != nil {
		panic(err)
	}

	roots := x509.NewCertPool()

	ok := roots.AppendCertsFromPEM(rootCA)
	if !ok {
		panic("failed to parse root certificate")
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      roots,
		// InsecureSkipVerify: false,
		// InsecureSkipVerify: true,
		// MinVersion:         tls.VersionTLS12,
	}

	return tlsConf
}

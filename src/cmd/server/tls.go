package main

import (
    "crypto/rand"
    "crypto/tls"
    "github.com/djaigoo/hole/src/pool"
    "github.com/djaigoo/logkit"
    "net"
    "time"
)

func tlsServer(listener net.Listener) {
    crt, err := tls.X509KeyPair(crtContent, keyContent)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    tlsConfig := &tls.Config{}
    tlsConfig.Certificates = []tls.Certificate{crt}
    // Time returns the current time as the number of seconds since the epoch.
    // If Time is nil, TLS uses time.Now.
    tlsConfig.Time = time.Now
    // Rand provides the source of entropy for nonces and RSA blinding.
    // If Rand is nil, TLS uses the cryptographic random reader in package
    // crypto/rand.
    // The Reader must be safe for use by multiple goroutines.
    tlsConfig.Rand = rand.Reader
    l := tls.NewListener(listener, tlsConfig)
    for {
        conn, err := l.Accept()
        if err != nil {
            logkit.Errorf("[tlsServer] accept error %s", err.Error())
            break
        }
        hc := pool.NewConn(conn)
        go handle(hc)
    }
}

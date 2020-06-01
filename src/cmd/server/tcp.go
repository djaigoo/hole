package main

import (
    "github.com/djaigoo/hole/src/pool"
    "github.com/djaigoo/logkit"
    "net"
)

func tcpServer(listener net.Listener) {
    for {
        conn, err := listener.Accept()
        if err != nil {
            logkit.Errorf("tcp Server accept conn error %s", err.Error())
            break
        }
        hc := pool.NewConn(conn)
        go handle(hc)
    }
}

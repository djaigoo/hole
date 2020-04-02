package main

import (
    "github.com/djaigoo/hole/src/code"
    "github.com/djaigoo/logkit"
    "net"
    "net/http"
)

func httpServer(listener net.Listener) {
    http.HandleFunc("/hello", hello)
    http.HandleFunc("/cfile", getCrtFile)
    http.HandleFunc("/kfile", getKeyFile)
    err := http.Serve(listener, nil)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
}

func hello(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("hello world"))
}

func getCrtFile(w http.ResponseWriter, r *http.Request) {
    logkit.Infof("get crt file %s", r.RemoteAddr)
    key := r.Header.Get("md5")
    if len(key) != 32 {
        logkit.Errorf("get crt file invalid md5 length %d md5 %s", len(key), key)
        return
    }
    w.Write([]byte(code.AesEncrypt(crtContent, []byte(key))))
}

func getKeyFile(w http.ResponseWriter, r *http.Request) {
    logkit.Infof("get key file %s", r.RemoteAddr)
    key := r.Header.Get("md5")
    if len(key) != 32 {
        logkit.Errorf("get crt file invalid md5 length %d md5 %s", len(key), key)
        return
    }
    w.Write([]byte(code.AesEncrypt(keyContent, []byte(key))))
}

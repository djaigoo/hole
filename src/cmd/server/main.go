package main

import (
    "flag"
    "github.com/djaigoo/hole/src/confs"
    "github.com/djaigoo/hole/src/dao"
    "github.com/djaigoo/hole/src/utils"
    "github.com/djaigoo/logkit"
    "github.com/soheilhy/cmux"
    "io/ioutil"
    "net"
    _ "net/http/pprof"
    "strconv"
    "time"
)

var (
    confpath   string
    crtContent []byte
    keyContent []byte
)

func init() {
    flag.StringVar(&confpath, "conf", "./config.toml", "配置文件")
    flag.Parse()
}

func main() {
    logkit.SingleFileLog("", "log", logkit.LevelDebug)
    defer func() {
        // del redis data
        dao.RedisDao.DelConnectKey()
        
        logkit.Exit()
    }()
    
    conf, err := confs.ReadConfigFile(confpath)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    
    crtContent, err = ioutil.ReadFile(conf.ServerCrtFile)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    keyContent, err = ioutil.ReadFile(conf.ServerKeyFile)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    logkit.Infof("[main] crt len %d, key len %d", len(crtContent), len(keyContent))
    
    listener, err := net.Listen("tcp", ":"+strconv.Itoa(conf.ServerPort))
    if err != nil {
        logkit.Errorf(err.Error())
        return
    }
    defer listener.Close()
    
    cm := cmux.New(listener)
    tlsListener := cm.Match(cmux.TLS())
    httpListener := cm.Match(cmux.HTTP1())
    
    go tlsServer(tlsListener)
    go httpServer(httpListener)
    time.Sleep(100 * time.Millisecond)
    go cm.Serve()
    
    if conf.Admin {
        go adminServer(conf.AdminPort, conf.RedisAddr, conf.RedisPassword)
    }
    
    logkit.Infof("start sever")
    logkit.Infof("server quit with signal %d", utils.Signal())
}

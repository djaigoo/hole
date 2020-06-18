package main

import (
    "flag"
    "github.com/djaigoo/hole/src/confs"
    "github.com/djaigoo/hole/src/connect"
    "github.com/djaigoo/hole/src/dao"
    "github.com/djaigoo/hole/src/pool"
    "github.com/djaigoo/hole/src/socks5"
    "github.com/djaigoo/hole/src/util"
    "github.com/djaigoo/logkit"
    "github.com/soheilhy/cmux"
    "io"
    "io/ioutil"
    "net"
    "strconv"
    "sync"
    "syscall"
    "time"
)

var (
    confpath   string
    crtContent []byte
    keyContent []byte
    
    conf *confs.Conf
)

func init() {
    flag.StringVar(&confpath, "conf", "./config.toml", "配置文件")
    flag.Parse()
}

func main() {
    var err error
    conf, err = confs.ReadConfigFile(confpath)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    
    if !conf.Debug {
        logkit.SingleFileLog("", "log", logkit.LevelDebug)
    }
    defer func() {
        // del redis data
        dao.RedisDao.DelConnectKey()
        
        logkit.Exit()
    }()
    
    logkit.Infof("conf: %#v", conf)
    
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
        logkit.Errorf("[main] listen error %s", err.Error())
        return
    }
    defer listener.Close()
    
    if conf.Mode == "tcp" {
        go tcpServer(listener)
    } else {
        cm := cmux.New(listener)
        tlsListener := cm.Match(cmux.TLS())
        httpListener := cm.Match(cmux.HTTP1())
        
        go tlsServer(tlsListener)
        go httpServer(httpListener)
        time.Sleep(100 * time.Millisecond)
        go cm.Serve()
    }
    
    if conf.Admin {
        if conf.Pprof {
            openPProf()
        }
        go adminServer(conf.AdminPort, conf.RedisAddr, conf.RedisPassword)
    }
    
    logkit.Infof("[main] start sever")
    logkit.Infof("[main] server quit with signal %d", util.Signal())
}

func handle(conn *pool.Conn) (err error) {
    close := true
    defer func() {
        if close {
            err := conn.Close()
            if err != nil {
                // logkit.Errorf("[handle] Close pool conn %s error %s", conn.RemoteAddr().String(), err.Error())
                return
            }
            logkit.Infof("[handle] Client %s Connection closed.....", conn.RemoteAddr().String())
        }
    }()
    dao.RedisDao.AddConnect(conn.RemoteAddr().String())
    defer dao.RedisDao.DelConnect(conn.RemoteAddr().String())
    logkit.Warnf("[handle] Receive Connect Request From %s", conn.RemoteAddr().String())
    
    // 清空连接缓冲区数据
    var attr *socks5.Attr
    for attr == nil {
        if conn.IsClose() {
            return
        }
        attr, err = socks5.GetAttrByConn(conn)
        if err != nil {
            if err == io.EOF {
                return
            }
            if err != pool.ErrInterrupt {
                logkit.Errorf("[handle] GetAttrByConn conn:%s %s", conn.RemoteAddr().String(), err.Error())
                return
            }
        }
        time.Sleep(500 * time.Millisecond)
    }
    
    info, _ := attr.Marshal()
    logkit.Infof("[handle] conn %s GetAttrByConn attr: %s info:%#v", conn.RemoteAddr().String(), attr.GetHost(), info)
    
    var remote net.Conn
    
    if attr.Command == socks5.Connect {
        host := attr.GetHost()
        remote, err = net.DialTimeout("tcp", host, 10*time.Second)
        if err != nil {
            if ne, ok := err.(*net.OpError); ok && (ne.Err == syscall.EMFILE || ne.Err == syscall.ENFILE) {
                // log too many open file error
                // EMFILE is process reaches open file limits, ENFILE is system limit
                logkit.Errorf("[handle] dial error: %s", err.Error())
            } else {
                logkit.Errorf("[handle] error connecting to: host %s, error %s", host, err.Error())
            }
            // 回收连接
            err = conn.Interrupt(10 * time.Second)
            if err != nil {
                logkit.Errorf("[handle] send interrupt conn:%s error %s", conn.RemoteAddr().String(), err.Error())
                return
            }
            close = false
            conn.Reset()
            logkit.Noticef("[handle] Recycle conn %s", conn.RemoteAddr().String())
            go handle(conn)
            return
        }
        logkit.Debugf("[handle] conn %s get tcp remote %s --> %s", conn.RemoteAddr().String(), remote.LocalAddr().String(), remote.RemoteAddr().String())
    } else if attr.Command == socks5.Udp {
        if !conf.StartUDP {
            // TODO UDP is not supported temporarily
            logkit.Warnf("UDP is not supported temporarily")
            // 回收连接
            err = conn.Interrupt(10 * time.Second)
            if err != nil {
                logkit.Errorf("[handle] send interrupt conn:%s error %s", conn.RemoteAddr().String(), err.Error())
                return
            }
            close = false
            conn.Reset()
            logkit.Noticef("[handle] Recycle conn %s", conn.RemoteAddr().String())
            go handle(conn)
            return
        }
        host := attr.GetHost()
        udpAddr, err := net.ResolveUDPAddr("udp", host)
        if err != nil {
            logkit.Errorf("[handle] ResolveUDPAddr host:%s error: %s", host, err.Error())
            return err
        }
        udpConn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}, udpAddr)
        if err != nil {
            logkit.Errorf("[handle] dial udp error: %s", err.Error())
            return err
        }
        logkit.Debugf("[handle] get udp conn %s --> %s", udpConn.LocalAddr().String(), udpConn.RemoteAddr().String())
        remote = udpConn
    } else {
        return
    }
    
    if remote == nil {
        return
    }
    defer func() {
        err = remote.Close()
        if err != nil {
            // logkit.Errorf("[handle] close remote %s --> %s error: %s", remote.LocalAddr().String(), remote.RemoteAddr().String(), err.Error())
            return
        }
    }()
    
    logkit.Debugf("[handle] get remote %s", remote.RemoteAddr().String())
    
    _, _, close = ServerCopy(remote, conn)
    logkit.Debugf("[handle] ServerCopy over")
    return
}

// src --> pool
func ServerCopy(dst net.Conn, src *pool.Conn) (n1, n2 int64, close bool) {
    active1 := false
    active2 := false
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            // 主动关闭外端连接 防止连接泄漏
            dst.SetReadDeadline(time.Now())
            wg.Done()
        }()
        n1, err := connect.Copy(dst, src)
        if err != nil {
            logkit.Infof("[ServerCopy] src:%s --> dst:%s write over %d byte error %s", src.RemoteAddr().String(), dst.RemoteAddr().String(), n1, err.Error())
        } else {
            logkit.Infof("[ServerCopy] src:%s --> dst:%s write over %d byte", src.RemoteAddr().String(), dst.RemoteAddr().String(), n1)
        }
        if err != nil && err != pool.ErrInterrupt {
            if operr, ok := err.(*net.OpError); ok {
                if operr.Op != "write" {
                    logkit.Errorf("[ServerCopy] src:%s --> dst:%s read error %s", src.RemoteAddr().String(), dst.RemoteAddr().String(), err.Error())
                    return
                }
            }
        }
        
        // src io.EOF
        if err == nil || src.IsClose() {
            logkit.Noticef("[ClientCopy] dst:%s --> %s status:%s", dst.LocalAddr().String(), dst.RemoteAddr().String(), src.Status())
            return
        }
        
        err = src.Interrupt(10 * time.Second)
        if err != nil {
            logkit.Errorf("[ServerCopy] src:%s send interrupt error %s", src.RemoteAddr().String(), err.Error())
            return
        }
        active1 = true
    }()
    
    go func() {
        defer func() {
            // 主动关闭外端连接 防止连接泄漏
            // dst.SetReadDeadline(time.Now())
            wg.Done()
        }()
        n2, err := connect.Copy(src, dst)
        if err != nil {
            logkit.Infof("[ServerCopy] dst:%s --> src:%s write over %d byte error %s", dst.RemoteAddr().String(), src.RemoteAddr().String(), n2, err.Error())
        } else {
            logkit.Infof("[ServerCopy] dst:%s --> src:%s write over %d byte", dst.RemoteAddr().String(), src.RemoteAddr().String(), n2)
        }
        
        if err != nil && err != pool.ErrInterrupt {
            if operr, ok := err.(*net.OpError); ok {
                if operr.Op == "write" {
                    logkit.Errorf("[ServerCopy] dst:%s --> src:%s write error %s", dst.RemoteAddr().String(), src.RemoteAddr().String(), err.Error())
                    return
                }
            }
        }
        
        if src.IsClose() {
            logkit.Noticef("[ClientCopy] dst:%s --> %s status:%s", dst.LocalAddr().String(), dst.RemoteAddr().String(), src.Status())
            return
        }
        
        err = src.Interrupt(10 * time.Second)
        if err != nil {
            logkit.Errorf("[ServerCopy] dst:%s --> src:%s send interrupt error %s", dst.RemoteAddr().String(), src.RemoteAddr().String(), err.Error())
            return
        }
        active2 = true
    }()
    wg.Wait()
    
    if active1 && active2 {
        logkit.Noticef("[ServerCopy] Recycle conn %s", src.RemoteAddr().String())
        close = false
        src.Reset()
        go handle(src)
    } else {
        logkit.Noticef("[ServerCopy] Remove conn %s active1:%v active2:%v", src.RemoteAddr().String(), active1, active2)
        close = true
    }
    logkit.Infof("[ServerCopy] OVER")
    return
}

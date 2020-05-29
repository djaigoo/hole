package main

import (
    "crypto/rand"
    "crypto/tls"
    "github.com/djaigoo/hole/src/dao"
    "github.com/djaigoo/hole/src/pool"
    "github.com/djaigoo/hole/src/socks5"
    "github.com/djaigoo/logkit"
    "io"
    "net"
    "sync"
    "syscall"
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

func handle(conn net.Conn) {
    close := true
    defer func() {
        if close {
            conn.Close()
        }
    }()
    dao.RedisDao.AddConnect(conn.RemoteAddr().String())
    defer dao.RedisDao.DelConnect(conn.RemoteAddr().String())
    logkit.Infof("[handle] Receive Connect Request From %s", conn.RemoteAddr().String())
    
    attr, err := socks5.GetAttrByConn(conn)
    if err != nil {
        if err != pool.ErrInterrupt {
            logkit.Errorf("[handle] GetAttrByConn %s", err.Error())
            return
        }
        // interrupt waiting ...
        for err == pool.ErrInterrupt {
            time.Sleep(500 * time.Millisecond)
            attr, err = socks5.GetAttrByConn(conn)
            if pool.CheckErr(err) {
                logkit.Errorf("[handle] GetAttrByConn %s", err.Error())
                return
            }
        }
    }
    if attr == nil {
        logkit.Errorf("attr is nil")
        return
    }
    info, _ := attr.Marshal()
    logkit.Infof("[handle] GetAttrByConn attr: %s info:%#v", attr.GetHost(), info)
    
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
            return
        }
        logkit.Debugf("[handle] get tcp conn %s --> %s", remote.LocalAddr().String(), remote.RemoteAddr().String())
    } else if attr.Command == socks5.Udp {
        if !conf.StartUDP {
            // TODO UDP is not supported temporarily
            return
        }
        host := attr.GetHost()
        udpAddr, err := net.ResolveUDPAddr("udp", host)
        if err != nil {
            logkit.Errorf("[handle] ResolveUDPAddr host:%s error: %s", host, err.Error())
            return
        }
        udpConn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}, udpAddr)
        if err != nil {
            logkit.Errorf("[handle] dial udp error: %s", err.Error())
            return
        }
        logkit.Debugf("[handle] get udp conn %s --> %s", udpConn.LocalAddr().String(), udpConn.RemoteAddr().String())
        remote = udpConn
    } else {
        return
    }
    
    if remote == nil {
        return
    }
    
    logkit.Debugf("[handle] get remote %s", remote.RemoteAddr().String())
    
    _, _, close, err = ServerCopy(remote, conn)
    if err != nil {
        logkit.Errorf("[handle] %s --> %s Pipe error %s", conn.RemoteAddr().String(), remote.RemoteAddr().String(), err.Error())
        return
    }
    logkit.Infof("[handle] Client %s Connection Closed.....", conn.RemoteAddr().String())
}

// src --> pool
func ServerCopy(dst, src net.Conn) (n1, n2 int64, close bool, err error) {
    active := true
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer wg.Done()
        n1, err = io.Copy(dst, src)
        if err != nil {
            logkit.Errorf("[ServerCopy] src:%s --> dst:%s write error %s", src.RemoteAddr().String(), dst.RemoteAddr().String(), err.Error())
            if operr, ok := err.(*net.OpError); ok {
                if operr.Err != pool.ErrInterrupt {
                    src.Close()
                    dst.Close()
                } else {
                    if hc, ok := src.(*pool.Conn); ok {
                        close = false
                        go handle(hc)
                    }
                }
            }
            return
        }
        src.Close()
        logkit.Infof("[ServerCopy] src:%s --> dst:%s write over %d byte", src.RemoteAddr().String(), dst.RemoteAddr().String(), n1)
    }()
    
    go func() {
        defer wg.Done()
        n2, err = io.Copy(src, dst)
        if err != nil {
            logkit.Errorf("[ServerCopy] dst:%s --> src:%s write error %s", dst.RemoteAddr().String(), src.RemoteAddr().String(), err.Error())
            // read src error or write dst error
            if operr, ok := err.(*net.OpError); ok {
                logkit.Errorf("net op error %#v", operr)
                if operr.Op == "write" {
                    active = false
                }
            }
            dst.Close()
        }
        logkit.Infof("[ServerCopy] dst:%s --> src:%s write over %d byte", dst.RemoteAddr().String(), src.RemoteAddr().String(), n2)
        
        if active {
            if hc, ok := src.(*pool.Conn); ok {
                err = hc.Interrupt(5 * time.Second)
                if err != nil {
                    logkit.Errorf("[ServerCopy] close write conn1 send interrupt error %s", err.Error())
                    return
                }
                close = false
                go handle(hc)
            }
        }
        
    }()
    wg.Wait()
    logkit.Infof("[ServerCopy] OVER")
    return
}

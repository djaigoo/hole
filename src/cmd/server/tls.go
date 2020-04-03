package main

import (
    "context"
    "crypto/rand"
    "crypto/tls"
    "encoding/binary"
    "github.com/djaigoo/hole/src/connect"
    "github.com/djaigoo/hole/src/dao"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "io"
    "net"
    "strconv"
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
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    for {
        conn, err := l.Accept()
        if err != nil {
            logkit.Error(err.Error())
            continue
        }
        go handle(conn)
    }
}

func handle(conn net.Conn) {
    defer conn.Close()
    dao.RedisDao.AddConnect(conn.RemoteAddr().String())
    defer dao.RedisDao.DelConnect(conn.RemoteAddr().String())
    logkit.Infof("Receive Connect Request From %s", conn.RemoteAddr().String())
    remote, err := getRequest(conn)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    defer remote.Close()
    logkit.Debugf("get remote %s", remote.RemoteAddr().String())
    ctx, cancel := context.WithCancel(context.Background())
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            cancel()
            wg.Done()
        }()
        n, err := connect.Copy(ctx, remote, conn)
        if err != nil {
            logkit.Error(err.Error())
            return
        }
        logkit.Debugf("conn -> remote send %d byte", n)
    }()
    go func() {
        defer func() {
            cancel()
            wg.Done()
        }()
        n, err := connect.Copy(ctx, conn, remote)
        if err != nil {
            logkit.Error(err.Error())
            return
        }
        logkit.Debugf("remote -> conn send %d byte", n)
    }()
    wg.Wait()
    logkit.Infof("Client %s Connection Closed.....", conn.RemoteAddr().String())
}

func getRequest(conn net.Conn) (remote net.Conn, err error) {
    // buf size should at least have the same size with the largest possible
    // request size (when addrType is 3, domain name has at most 256 bytes)
    // 1(addrType) + 1(lenByte) + 255(max length address) + 2(port) + 10(hmac-sha1)
    buf := make([]byte, 269)
    // read till we get possible domain length field
    if _, err = io.ReadFull(conn, buf[:IdType+1]); err != nil {
        return
    }
    
    var reqStart, reqEnd int
    addrType := buf[IdType]
    switch addrType & AddrMask {
    case TypeIPv4:
        reqStart, reqEnd = IdIP0, IdIP0+LenIPv4
    case TypeIPv6:
        reqStart, reqEnd = IdIP0, IdIP0+LenIPv6
    case TypeDm:
        if _, err = io.ReadFull(conn, buf[IdType+1:IdDmLen+1]); err != nil {
            return
        }
        reqStart, reqEnd = IdDm0, IdDm0+int(buf[IdDmLen])+LenDmBase
    default:
        err = errors.Errorf("addr type %d not supported", addrType&AddrMask)
        return
    }
    
    // logkit.Debugf("start %d, end %d", reqStart, reqEnd)
    if _, err = io.ReadFull(conn, buf[reqStart:reqEnd]); err != nil {
        return
    }
    // logkit.Debugf("buf content %#v", buf[reqStart:reqEnd])
    // Return string for typeIP is not most efficient, but browsers (Chrome,
    // Safari, Firefox) all seems using TypeDm exclusively. So this is not a
    // big problem.
    host := ""
    switch addrType & AddrMask {
    case TypeIPv4:
        host = net.IP(buf[IdIP0 : IdIP0+net.IPv4len]).String()
    case TypeIPv6:
        host = net.IP(buf[IdIP0 : IdIP0+net.IPv6len]).String()
    case TypeDm:
        host = string(buf[IdDm0 : IdDm0+int(buf[IdDmLen])])
    }
    // parse port
    port := binary.BigEndian.Uint16(buf[reqEnd-2 : reqEnd])
    host = net.JoinHostPort(host, strconv.Itoa(int(port)))
    logkit.Debugf("remote addr %s", host)
    
    remote, err = net.Dial("tcp", host)
    if err != nil {
        if ne, ok := err.(*net.OpError); ok && (ne.Err == syscall.EMFILE || ne.Err == syscall.ENFILE) {
            // log too many open file error
            // EMFILE is process reaches open file limits, ENFILE is system limit
            logkit.Errorf("dial error: %s", err.Error())
        } else {
            logkit.Errorf("error connecting to: host %s, error %s", host, err.Error())
        }
        return
    }
    return
}

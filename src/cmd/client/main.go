package main

import (
    "crypto/rand"
    "crypto/tls"
    "encoding/binary"
    "flag"
    "fmt"
    "github.com/djaigoo/hole/src/code"
    "github.com/djaigoo/hole/src/confs"
    "github.com/djaigoo/hole/src/connect"
    "github.com/djaigoo/hole/src/pool"
    "github.com/djaigoo/hole/src/socks5"
    "github.com/djaigoo/hole/src/util"
    "github.com/djaigoo/httpclient"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "net"
    "net/http"
    "strconv"
    "strings"
    "sync"
    "time"
)

var (
    confpath   string
    listenPort int
    remoteAddr string
    debug      bool
    udp        bool
    psize      int
    mode       string
    dtimeout   int // 拨号超时
)

func init() {
    flag.StringVar(&confpath, "conf", "", "配置文件")
    flag.IntVar(&listenPort, "port", 1086, "本地监听地址")
    flag.StringVar(&remoteAddr, "addr", "", "远端服务器地址")
    flag.BoolVar(&debug, "debug", false, "是否打印调试日志")
    flag.BoolVar(&udp, "udp", false, "是否启动udp")
    flag.IntVar(&psize, "psize", 100, "连接池大小")
    flag.StringVar(&mode, "mode", "tls", "连接模式")
    flag.IntVar(&dtimeout, "dtimeout", 10, "拨号超时")
    flag.Parse()
}

func main() {
    defer logkit.Exit()
    var err error
    conf := &confs.Conf{}
    if confpath != "" {
        conf, err = confs.ReadConfigFile(confpath)
        if err != nil {
            logkit.Error(err.Error())
            return
        }
    }
    if listenPort != 0 {
        conf.LocalPort = listenPort
    }
    if remoteAddr != "" {
        i := strings.Index(remoteAddr, ":")
        if i < 0 {
            logkit.Errorf("[main] invalid addr %s", remoteAddr)
            return
        }
        conf.Server = remoteAddr[:i]
        conf.ServerPort, err = strconv.Atoi(remoteAddr[i+1:])
        if err != nil {
            logkit.Errorf("[main] invalid addr %s", remoteAddr)
            return
        }
    }
    if debug {
        conf.Debug = true
    }
    
    level := logkit.LevelNon
    if conf.Debug {
        level = logkit.LevelDebug
    }
    logkit.ConsoleLog(level)
    logkit.Debugf("[main] print conf %#v", conf)
    
    addr := conf.Server + ":" + strconv.Itoa(conf.ServerPort)
    
    config := &tls.Config{}
    config.InsecureSkipVerify = true // 跳过安全验证，可以输入与证书不同的域名
    if mode == "tcp" {
        config = nil
    }
    
    // start connect pool
    dial := &net.Dialer{
        Timeout:   time.Duration(dtimeout) * time.Second,
        KeepAlive: 1 * time.Minute,
    }
    pool.Start(dial, addr, psize, config)
    defer pool.Close()
    
    listener, err := net.Listen("tcp", ":"+strconv.Itoa(conf.LocalPort))
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    defer func() {
        err = listener.Close()
        if err != nil {
            logkit.Errorf("[main] Close listener error %s", err.Error())
            return
        }
        logkit.Infof("[main] Close listener success")
    }()
    go func() {
        logkit.Infof("[main] start listen %d", conf.LocalPort)
        for {
            conn, err := listener.Accept()
            if err != nil {
                logkit.Error(err.Error())
                return
            }
            go handle(conn)
        }
    }()
    if udp {
        go startUDP(conf.LocalPort)
    }
    
    logkit.Infof("[main] client quit with signal %d", util.Signal())
}

func startUDP(port int) {
    logkit.Infof("[main] start listen udp port %d", port)
    conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
    if err != nil {
        logkit.Infof("[main] listenUDP error %s", err.Error())
        return
    }
    defer conn.Close()
    
    data := make([]byte, 2048)
    for {
        n, addr, err := conn.ReadFromUDP(data)
        if err != nil {
            logkit.Errorf("[main] udp read from error %s", err.Error())
            return
        }
        // logkit.Infof("get udp remote %s msg %#v", addr.String(), data[:n])
        if n < 3 {
            return
        }
        msg := make([]byte, n)
        copy(msg, data[:n])
        
        go handleUDP(conn, addr, msg)
    }
}

func handleUDP(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
    n := len(data)
    site := 0
    site += 2
    flag := data[site]
    _ = flag
    site++
    atyp := data[site]
    site++
    var host string
    var port []byte
    switch atyp {
    case socks5.IPv4:
        if n < site+6 {
            return
        }
        addr := data[site : site+4]
        site += 4
        port = data[site : site+2]
        site += 2
        host = net.IP(addr).String()
    case socks5.Domain:
        if n < site+1 {
            return
        }
        l := data[site]
        site++
        addr := data[site : site+int(l)]
        site += int(l)
        port = data[site : site+2]
        site += 2
        host = string(addr)
    case socks5.IPv6:
        if n < site+18 {
            return
        }
        addr := data[site : site+16]
        site += 16
        port = data[site : site+2]
        site += 2
        host = net.IP(addr).String()
    }
    host += ":" + strconv.Itoa(int(binary.BigEndian.Uint16(port)))
    
    logkit.Debugf("[handleUDP] conn %s dial udp to host %s site %d data len %d", addr.String(), host, site, n)
    raddr, err := net.ResolveUDPAddr("udp", host)
    if err != nil {
        logkit.Errorf("[handleUDP] ResolveUDPAddr error %s", err.Error())
        return
    }
    
    uconn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}, raddr)
    if err != nil {
        logkit.Errorf("[handleUDP] DialUDP raddr:%s error %s", raddr, err.Error())
        return
    }
    defer uconn.Close()
    uconn.SetWriteDeadline(time.Now().Add(10 * time.Second))
    // logkit.Alertf("uconn:%s->%s write data %#v", uconn.LocalAddr().String(), uconn.RemoteAddr().String(), data[site:])
    n, err = uconn.Write(data[site:])
    if err != nil {
        logkit.Errorf("[handleUDP] write uconn:%s->%s error %s", uconn.LocalAddr().String(), uconn.RemoteAddr().String(), err.Error())
        return
    }
    msg := make([]byte, 2048)
    uconn.SetReadDeadline(time.Now().Add(10 * time.Second))
    n, _, err = uconn.ReadFrom(msg)
    if err != nil {
        logkit.Errorf("[handleUDP] read uconn:%s->%s error %s", uconn.LocalAddr().String(), uconn.RemoteAddr().String(), err.Error())
        return
    }
    logkit.Debugf("[handleUDP] write to udp %s --> %s %d byte", addr.String(), raddr.String(), n)
    
    conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
    // logkit.Alertf("conn:%s->%s write data %#v", uconn.LocalAddr().String(), uconn.RemoteAddr().String(), append(data[:site], msg[:n]...))
    _, err = conn.WriteToUDP(append(data[:site], msg[:n]...), addr)
    if err != nil {
        logkit.Errorf("[handleUDP] write udp conn:%s->%s error %s", conn.LocalAddr().String(), conn.RemoteAddr().String(), err.Error())
        return
    }
}

func getCA(addr string) (crtData []byte, keyData []byte, err error) {
    keybuf := make([]byte, 16)
    n, _ := rand.Read(keybuf)
    if n != 16 {
        return nil, nil, errors.Errorf("not reading enough length %d", n)
    }
    key := fmt.Sprintf("%x", keybuf)
    http.DefaultClient.Timeout = 5 * time.Second
    logkit.Debugf("[getCA] send md5 key: %s", key)
    cdata, err := httpclient.Get("http://"+addr+"/cfile").SetHeader("md5", key).Do().ToText()
    if err != nil {
        return
    }
    if len(cdata) == 0 {
        return nil, nil, errors.Errorf("get cfile resp len 0")
    }
    crtData = code.AesDecrypt(string(cdata), []byte(key))
    
    kdata, err := httpclient.Get("http://"+addr+"/kfile").SetHeader("md5", key).Do().ToText()
    if err != nil {
        return
    }
    if len(kdata) == 0 {
        return nil, nil, errors.Errorf("get kfile resp len 0")
    }
    keyData = code.AesDecrypt(string(kdata), []byte(key))
    
    logkit.Infof("[getCA] get ca success")
    
    // close all default client
    http.DefaultClient.CloseIdleConnections()
    return
}

func handle(conn net.Conn) {
    defer func() {
        err := conn.Close()
        if err != nil {
            // logkit.Errorf("[handle] close local connect %s --> %s error %s", conn.RemoteAddr().String(), conn.LocalAddr().String(), err.Error())
            return
        }
        logkit.Warnf("[handle] local connect close %s --> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
    }()
    logkit.Infof("[handle] get new request %s", conn.RemoteAddr())
    attr := &socks5.Attr{}
    err := attr.Handshake(conn)
    if err != nil {
        logkit.Errorf("[handle] attr handshake remote:%s error %s", conn.RemoteAddr().String(), err.Error())
        return
    }
    
    rawAddr, _ := attr.Marshal()
    server, err := pool.Get()
    if err != nil {
        logkit.Errorf("[handle] pool get %s", err.Error())
        return
    }
    
    _, err = server.Write(rawAddr)
    if err != nil {
        logkit.Errorf("[handle] %s --> %s server write raw addr error %s", conn.RemoteAddr().String(), server.RemoteAddr(), err.Error())
        pool.Remove(server, pool.RWriteErr)
        return
    }
    logkit.Debugf("[handle] send rawAddr %#v", rawAddr)
    logkit.Debugf("[handle] client connect %s --> %s host %s", conn.RemoteAddr().String(), server.LocalAddr().String(), attr.GetHost())
    
    // logkit.Warnf("[handle] conn info %s --> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
    _, _ = ClientCopy(server, conn)
    
    logkit.Debugf("[handle] close conn %s remote %s", conn.RemoteAddr().String(), server.LocalAddr().String())
}

type stype uint8

func (s stype) String() string {
    switch s {
    case sDef:
        return "sDef"
    case sErr:
        return "sErr"
    case sClose:
        return "sClose"
    case sActive:
        return "sActive"
    default:
        return strconv.Itoa(int(s))
    }
    return ""
}

const (
    sDef = stype(iota)
    sErr
    sClose
    sBeClosed
    sActive
)

// dst --> pool
func ClientCopy(dst *pool.Conn, src net.Conn) (n1, n2 int64) {
    back1 := sDef // 默认dst连接直接remove 改成状态值 退出Copy时检测
    back2 := sDef
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            // 主动关闭外端连接 防止连接不释放
            // src.SetReadDeadline(time.Now())
            wg.Done()
        }()
        n1, err := connect.Copy(dst, src)
        dst.AddWriteBytes(n1)
        if err != nil {
            logkit.Infof("[ClientCopy] src:%s --> dst:%s write over %d byte error %s", src.RemoteAddr().String(), dst.LocalAddr().String(), n1, err.Error())
        } else {
            logkit.Infof("[ClientCopy] src:%s --> dst:%s write over %d byte", src.RemoteAddr().String(), dst.LocalAddr().String(), n1)
        }
        if err != nil && err != pool.ErrInterrupt {
            // only write dst error return
            if operr, ok := err.(*net.OpError); ok {
                if operr.Op == "write" {
                    logkit.Errorf("[ClientCopy] src:%s --> dst:%s write error %s", src.RemoteAddr().String(), dst.LocalAddr().String(), err.Error())
                    back1 = sErr
                    return
                }
            }
        }
        
        if dst.IsClose() {
            logkit.Noticef("[ClientCopy] dst:%s is closed status:%s", dst.LocalAddr().String(), dst.Status())
            back1 = sClose
            return
        }
        
        if back2 == sErr || back2 == sClose {
            logkit.Noticef("[ClientCopy] dst:%s is quit back2:%s", dst.LocalAddr().String(), back2)
            back1 = sBeClosed
            return
        }
        
        err = dst.Interrupt(10 * time.Second)
        if err != nil {
            logkit.Errorf("[ClientCopy] src:%s --> dst:%s send interrupt status %s error %s", src.RemoteAddr().String(), dst.LocalAddr().String(), dst.Status(), err.Error())
            back1 = sErr
            return
        }
        back1 = sActive
    }()
    
    go func() {
        defer func() {
            // 主动关闭外端连接 防止连接不释放
            src.SetReadDeadline(time.Now())
            wg.Done()
        }()
        n2, err := connect.Copy(src, dst)
        dst.AddReadBytes(n2)
        if err != nil {
            logkit.Infof("[ClientCopy] dst:%s --> src:%s write over %d byte error %s", dst.LocalAddr().String(), src.RemoteAddr().String(), n2, err.Error())
        } else {
            logkit.Infof("[ClientCopy] dst:%s --> src:%s write over %d byte", dst.LocalAddr().String(), src.RemoteAddr().String(), n2)
        }
        if err != nil && err != pool.ErrInterrupt {
            if operr, ok := err.(*net.OpError); ok {
                if operr.Op != "write" {
                    logkit.Errorf("[ClientCopy] dst:%s --> src:%s write error %s", dst.LocalAddr().String(), src.RemoteAddr().String(), err.Error())
                    back2 = sErr
                    return
                }
            }
        }
        
        if err == nil || dst.IsClose() {
            logkit.Noticef("[ClientCopy] dst:%s is closed status:%s", dst.LocalAddr().String(), dst.Status())
            back2 = sClose
            return
        }
        
        if back1 == sErr || back1 == sClose {
            logkit.Noticef("[ClientCopy] dst:%s is quit back1:%s", dst.LocalAddr().String(), back1)
            back2 = sBeClosed
            return
        }
        
        err = dst.Interrupt(10 * time.Second)
        if err != nil {
            logkit.Errorf("[ClientCopy] dst:%s --> src:%s send interrupt status %s error %s", dst.LocalAddr().String(), src.RemoteAddr().String(), dst.Status(), err.Error())
            back2 = sErr
            return
        }
        
        back2 = sActive
    }()
    wg.Wait()
    
    if back1 == sActive && back2 == sActive {
        logkit.Noticef("[ClientCopy] Recycle conn %s", src.RemoteAddr().String())
        pool.Put(dst)
    } else {
        logkit.Debugf("[ClientCopy] Remove conn %s --> %s back1:%v back2:%v", dst.LocalAddr().String(), dst.RemoteAddr().String(), back1, back2)
        if back1 != sActive {
            pool.Remove(dst, pool.RWriteErr)
        } else {
            pool.Remove(dst, pool.RReadErr)
        }
    }
    logkit.Infof("[ClientCopy] OVER conn src:%s --> dst:%s", src.RemoteAddr().String(), dst.LocalAddr().String())
    return
}

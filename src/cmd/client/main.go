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
    "github.com/djaigoo/hole/src/socks5"
    "github.com/djaigoo/hole/src/utils"
    "github.com/djaigoo/httpclient"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "io"
    "net"
    "net/http"
    "strconv"
    "strings"
    "time"
)

var (
    confpath   string
    listenPort int
    remoteAddr string
    debug      bool
    udp        bool
)

func init() {
    flag.StringVar(&confpath, "conf", "", "配置文件")
    flag.IntVar(&listenPort, "port", 1086, "本地监听地址")
    flag.StringVar(&remoteAddr, "addr", "", "远端服务器地址")
    flag.BoolVar(&debug, "debug", false, "是否打印调试日志")
    flag.BoolVar(&udp, "udp", false, "是否启动udp")
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
    // get ca
    crtData, keyData, err := getCA(addr)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    cert, err := tls.X509KeyPair(crtData, keyData)
    // cert, err := tls.LoadX509KeyPair(conf.LocalCrtFile, conf.LocalKeyFile)
    if err != nil {
        logkit.Error(err.Error())
        return
    }
    config := &tls.Config{}
    config.Rand = rand.Reader
    config.Certificates = append(config.Certificates, cert)
    config.InsecureSkipVerify = true // 跳过安全验证，可以输入与证书不同的域名
    
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
            go handle(conn, addr, config)
        }
    }()
    if udp {
        go func() {
            port := conf.LocalPort
            logkit.Infof("[main] start listen udp port %d", port)
            ulistener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
            if err != nil {
                logkit.Infof("[main] listenUDP error %s", err.Error())
                return
            }
            defer ulistener.Close()
            
            data := make([]byte, 2048)
            for {
                n, addr, err := ulistener.ReadFromUDP(data)
                if err != nil {
                    logkit.Errorf("udp read from error %s", err.Error())
                    return
                }
                // logkit.Infof("get udp remote %s msg %#v", addr.String(), data[:n])
                if n < 3 {
                    return
                }
                msg := make([]byte, n)
                copy(msg, data[:n])
                go handleUDP(addr, msg)
            }
        }()
    }
    
    logkit.Infof("client quit with signal %d", utils.Signal())
}

func handleUDP(addr *net.UDPAddr, data []byte) {
    n := len(data)
    site := 0
    site += 2
    flag := data[site]
    flag = flag
    site++
    atyp := data[site]
    site++
    var host string
    switch atyp {
    case socks5.IPv4:
        if n < site+6 {
            return
        }
        addr := data[site : site+4]
        site += 4
        port := data[site : site+2]
        site += 2
        host = net.IP(addr).String() + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(port)))
    case socks5.Domain:
        if n < site+1 {
            return
        }
        l := data[site]
        site++
        addr := data[site : site+int(l)]
        site += int(l)
        port := data[site : site+2]
        site += 2
        host = string(addr) + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(port)))
    case socks5.IPv6:
        if n < site+18 {
            return
        }
        addr := data[site : site+16]
        site += 16
        port := data[site : site+2]
        site += 2
        host = net.IP(addr).String() + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(port)))
    }
    
    logkit.Infof("dial to host %s", host)
    raddr, err := net.ResolveUDPAddr("udp", host)
    if err != nil {
        logkit.Errorf("%s", err.Error())
        return
    }
    uconn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}, raddr)
    if err != nil {
        logkit.Errorf("%s", err.Error())
        return
    }
    defer uconn.Close()
    uconn.SetWriteDeadline(time.Now().Add(5 * time.Second))
    n, err = uconn.Write(data[site:])
    if err != nil {
        logkit.Errorf("%s", err.Error())
        return
    }
    msg := make([]byte, 2048)
    uconn.SetReadDeadline(time.Now().Add(5 * time.Second))
    n, err = uconn.Read(msg)
    if err != nil {
        logkit.Errorf("%s", err.Error())
        return
    }
    logkit.Infof("write to udp %s --> %s %d byte", addr.String(), raddr.String(), n)
    uconn.SetWriteDeadline(time.Now().Add(5 * time.Second))
    _, err = uconn.WriteToUDP(append(data[:site], msg[:n]...), addr)
    if err != nil {
        logkit.Errorf("%s", err.Error())
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

func handle(conn net.Conn, addr string, config *tls.Config) {
    defer func() {
        err := conn.Close()
        if err != nil {
            logkit.Errorf("[handle] close local connect %s --> %s error %s", conn.RemoteAddr().String(), conn.LocalAddr().String(), err.Error())
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
    
    server, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, config)
    if err != nil {
        logkit.Errorf("[handle] tls dial %s", err.Error())
        return
    }
    defer func() {
        err := server.Close()
        if err != nil {
            logkit.Errorf("[handle] close remote connect %s -> %s error %s", server.LocalAddr().String(), server.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Warnf("[handle] remote connect close %s --> %s", server.LocalAddr().String(), server.RemoteAddr().String())
    }()
    
    logkit.Infof("[handle] client connect %s --> %s", conn.LocalAddr().String(), server.RemoteAddr().String())
    
    _, err = server.Write(rawAddr)
    if err != nil {
        logkit.Errorf("[handle] %s --> %s server write raw addr error %s", conn.RemoteAddr().String(), server.RemoteAddr(), err.Error())
        return
    }
    logkit.Infof("[handle] send rawAddr %#v", rawAddr)
    
    _, _, err = connect.Pipe(conn, server)
    if err != nil {
        logkit.Errorf("[handle] Pipe %s --> %s error %s", conn.RemoteAddr().String(), server.RemoteAddr().String(), err.Error())
        return
    }
    logkit.Debugf("[handle] close conn %s remote %s", conn.RemoteAddr().String(), server.RemoteAddr().String())
}

func handShake(conn net.Conn) (err error) {
    const (
        idVer     = 0
        idNmethod = 1
    )
    // version identification and method selection message in theory can have
    // at most 256 methods, plus version and nmethod field in total 258 bytes
    // the current rfc defines only 3 authentication methods (plus 2 reserved),
    // so it won't be such long in practice
    
    buf := make([]byte, 258)
    
    var n int
    // make sure we get the nmethod field
    if n, err = io.ReadAtLeast(conn, buf, idNmethod+1); err != nil {
        return
    }
    if buf[idVer] != socksVer5 {
        return errVer
    }
    nmethod := int(buf[idNmethod])
    msgLen := nmethod + 2
    if n == msgLen { // handshake done, common case
        // do nothing, jump directly to send confirmation
    } else if n < msgLen { // has more methods to read, rare case
        if _, err = io.ReadFull(conn, buf[n:msgLen]); err != nil {
            return
        }
    } else { // error, should not get extra data
        return errAuthExtraData
    }
    // send confirmation: version 5, no authentication required
    _, err = conn.Write([]byte{socksVer5, 0})
    return
}

// getRequest 获取请求数据
// rawaddr返回请求地址，IPv4 IPv6 域名
// host 返回目标IP:port
func getRequest(conn net.Conn) (rawaddr []byte, err error) {
    // refer to getRequest in server.go for why set buffer size to 263
    buf := make([]byte, 263)
    var n int
    // read till we get possible domain length field
    if n, err = io.ReadAtLeast(conn, buf, IdDmLen+1); err != nil {
        return
    }
    // check version and cmd
    if buf[IdVer] != socksVer5 {
        err = errVer
        return
    }
    if buf[IdCmd] != socksCmdConnect {
        err = errCmd
        logkit.Errorf("[getRequest] invalid cmd %d", buf[IdCmd])
        return
    }
    
    reqLen := -1
    switch buf[IdType] {
    case TypeIPv4:
        reqLen = LenIPv4
    case TypeIPv6:
        reqLen = LenIPv6
    case TypeDm:
        reqLen = int(buf[IdDmLen]) + LenDmBase
    default:
        err = errAddrType
        return
    }
    
    if n == reqLen {
        // common case, do nothing
    } else if n < reqLen { // rare case
        if _, err = io.ReadFull(conn, buf[n:reqLen]); err != nil {
            return
        }
    } else {
        err = errReqExtraData
        return
    }
    
    rawaddr = buf[IdType:reqLen]
    
    // print host
    host := ""
    port := strconv.Itoa(int(binary.BigEndian.Uint16(buf[reqLen-2:])))
    switch rawaddr[0] {
    case TypeIPv4:
        host = (&net.IPAddr{IP: buf[IdType+1 : reqLen-2]}).String() + ":" + port
    case TypeIPv6:
        host = (&net.IPAddr{IP: buf[IdType+1 : reqLen-2]}).String() + ":" + port
    case TypeDm:
        host = string(buf[IdType+2:reqLen-2]) + ":" + port
    }
    logkit.Debugf("[getRequest] %s request host %s", conn.RemoteAddr(), host)
    return
}

package connect

import (
    "crypto/tls"
    "github.com/djaigoo/logkit"
    "io"
    "net"
    "sync"
)

// Pipe 完成两个连接数据互通
func Pipe(conn1, conn2 net.Conn) (n1, n2 int64, err error) {
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            err = CloseWrite(conn2)
            if err != nil {
                logkit.Errorf("close write conn2 error %s", err.Error())
            }
            wg.Done()
        }()
        n1, err = io.Copy(conn2, conn1)
        if err != nil {
            logkit.Errorf("[Pipe] %s --> %s write error %s", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Infof("[Pipe] %s --> %s write over %d byte", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), n1)
    }()
    
    go func() {
        defer func() {
            err = CloseWrite(conn1)
            if err != nil {
                logkit.Errorf("close write conn1 error %s", err.Error())
            }
            wg.Done()
        }()
        n2, err = io.Copy(conn1, conn2)
        if err != nil {
            logkit.Errorf("[Pipe] %s --> %s write error %s", conn2.RemoteAddr().String(), conn1.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Infof("[Pipe] %s --> %s write over %d byte", conn2.RemoteAddr().String(), conn1.RemoteAddr().String(), n2)
    }()
    wg.Wait()
    logkit.Infof("[Pipe] OVER")
    return
}

func CloseWrite(conn net.Conn) error {
    switch conn.(type) {
    case *net.TCPConn:
        logkit.Warnf("close write tcp conn")
        tconn := conn.(*net.TCPConn)
        return tconn.CloseWrite()
    case *tls.Conn:
        logkit.Warnf("close write tls conn")
        tconn := conn.(*tls.Conn)
        return tconn.CloseWrite()
    default:
        logkit.Warnf("invalid conn type %#v", conn)
        return conn.Close()
    }
}

package connect

import (
    "crypto/tls"
    "github.com/djaigoo/logkit"
    "io"
    "net"
)

// Copy 完成两个连接数据互通
func Copy(dst, src net.Conn) (written int64, err error) {
    size := 32 * 1024
    buf := make([]byte, size)
    for {
        nr, er := src.Read(buf)
        if nr > 0 {
            nw, ew := dst.Write(buf[0:nr])
            if nw > 0 {
                written += int64(nw)
            }
            if ew != nil {
                err = ew
                break
            }
            if nr != nw {
                err = io.ErrShortWrite
                break
            }
        }
        if er != nil {
            if er != io.EOF {
                err = er
            }
            break
        }
    }
    return written, err
}

func CloseWrite(conn net.Conn) error {
    switch conn.(type) {
    case *net.TCPConn:
        logkit.Warnf("[CloseWrite] close write tcp conn %s --> %s", conn.LocalAddr().String(), conn.RemoteAddr().String())
        tconn := conn.(*net.TCPConn)
        return tconn.CloseWrite()
    case *tls.Conn:
        logkit.Warnf("[CloseWrite] close write tls conn %s --> %s", conn.LocalAddr().String(), conn.RemoteAddr().String())
        tconn := conn.(*tls.Conn)
        return tconn.CloseWrite()
    case *net.UDPConn:
        logkit.Warnf("[CloseWrite] close write udp conn %s --> %s", conn.LocalAddr().String(), conn.RemoteAddr().String())
        tconn := conn.(*net.UDPConn)
        return tconn.Close()
    default:
        logkit.Errorf("[CloseWrite] invalid conn %s --> %s type %#v", conn.LocalAddr().String(), conn.RemoteAddr().String(), conn)
        return conn.Close()
    }
}

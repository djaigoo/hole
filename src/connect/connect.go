package connect

import (
    "github.com/djaigoo/logkit"
    "io"
    "net"
    "sync"
    "time"
)

var (
    timeout = 3 * time.Minute
)

func Copy(dst net.Conn, src net.Conn) (written int64, err error) {
    size := 32 * 1024
    buf := make([]byte, size)
    for {
        // src.SetReadDeadline(time.Now().Add(timeout))
        nr, er := src.Read(buf)
        if nr > 0 {
            // dst.SetWriteDeadline(time.Now().Add(timeout))
            nw, ew := dst.Write(buf[0:nr])
            if nw > 0 {
                written += int64(nw)
            }
            logkit.Debugf("[Copy] %s --> %s write %d byte", src.RemoteAddr().String(), dst.RemoteAddr().String(), nw)
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
            if er == io.EOF {
                break
            }
            err = er
            break
        }
    }
    return
}

func Pipe(conn1, conn2 net.Conn) (n1, n2 int64, c1Close, c2Close bool, err error) {
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            err = conn2.Close()
            if err != nil {
                logkit.Errorf("[Pipe] close %s --> %s error %s", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), err.Error())
                return
            }
            c2Close = true
            wg.Done()
        }()
        n1, err = Copy(conn2, conn1)
        if err != nil {
            logkit.Errorf("[Pipe] %s --> %s write error %s", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Infof("[Pipe] %s --> %s write over %d byte", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), n1)
    }()
    
    go func() {
        defer func() {
            err = conn1.Close()
            if err != nil {
                logkit.Errorf("[Pipe] close %s --> %s error %s", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), err.Error())
                return
            }
            c1Close = true
            wg.Done()
        }()
        n2, err = Copy(conn1, conn2)
        if err != nil {
            logkit.Errorf("[Pipe] %s --> %s write error %s", conn2.RemoteAddr().String(), conn1.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Infof("[Pipe] %s --> %s write over %d byte", conn2.RemoteAddr().String(), conn1.RemoteAddr().String(), n2)
    }()
    wg.Wait()
    return
}

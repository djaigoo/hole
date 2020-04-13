package connect

import (
    "context"
    "github.com/djaigoo/logkit"
    "io"
    "net"
    "strings"
    "sync"
    "time"
)

var (
    timeout = 3 * time.Second
)

func Copy(ctx context.Context, dst net.Conn, src net.Conn) (written int64, err error) {
    size := 16 * 1024
    buf := make([]byte, size)
    for {
        select {
        case <-ctx.Done():
            logkit.Warnf("[Copy] context done")
            return
        default:
            src.SetReadDeadline(time.Now().Add(timeout))
            nr, er := src.Read(buf)
            if nr > 0 {
                nw, ew := dst.Write(buf[0:nr])
                if nw > 0 {
                    written += int64(nw)
                }
                logkit.Debugf("[Copy] %s --> %s write %d byte", src.RemoteAddr().String(), dst.RemoteAddr().String(), nw)
                if ew != nil {
                    err = ew
                    return
                }
                if nr != nw {
                    err = io.ErrShortWrite
                    return
                }
            }
            if er != nil {
                if er == io.EOF {
                    return
                }
                if e, ok := er.(*net.OpError); ok {
                    if strings.Contains(e.Error(), "i/o timeout") {
                        continue
                    }
                }
                err = er
                return
            }
        }
    }
}

func Pipe(conn1, conn2 net.Conn) (n1, n2 int64, c1Close, c2Close bool, err error) {
    ctx, cancel := context.WithCancel(context.Background())
    wg := new(sync.WaitGroup)
    wg.Add(2)
    go func() {
        defer func() {
            cancel()
            wg.Done()
        }()
        n1, err = Copy(ctx, conn2, conn1)
        if err != nil {
            logkit.Errorf("[Pipe] %s --> %s write error %s", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), err.Error())
            return
        }
        logkit.Infof("[Pipe] %s --> %s write over %d byte", conn1.RemoteAddr().String(), conn2.RemoteAddr().String(), n1)
    }()
    
    go func() {
        defer func() {
            cancel()
            wg.Done()
        }()
        n2, err = Copy(ctx, conn1, conn2)
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

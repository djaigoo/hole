package connect

import (
    "context"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "io"
    "net"
    "time"
)

var (
    timeout = 10 * time.Second
)

func Copy(ctx context.Context, dst net.Conn, src net.Conn) (written int64, err error) {
    size := 32 * 1024
    buf := make([]byte, size)
res:
    for {
        select {
        case <-ctx.Done():
            break res
        case <-time.After(1 * time.Minute):
            // 设置读写超时
            err = errors.New("[Copy] Copy i/o timeout")
            break res
        default:
            src.SetReadDeadline(time.Now().Add(timeout))
            nr, er := src.Read(buf)
            if nr > 0 {
                dst.SetWriteDeadline(time.Now().Add(timeout))
                nw, ew := dst.Write(buf[0:nr])
                if nw > 0 {
                    written += int64(nw)
                }
                logkit.Debugf("[Copy] %s --> %s write %d byte", src.RemoteAddr().String(), dst.RemoteAddr().String(), nw)
                if ew != nil {
                    err = ew
                    break res
                }
                if nr != nw {
                    err = io.ErrShortWrite
                    break res
                }
            }
            if er != nil {
                if er != io.EOF {
                    err = er
                }
                break res
            }
        }
    }
    return
}

func Pipe(ctx context.Context, p1, p2 io.ReadWriter) (err error) {
    return
}

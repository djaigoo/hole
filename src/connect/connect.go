package connect

import (
    "context"
    "github.com/pkg/errors"
    "io"
    "time"
)

func Copy(ctx context.Context, dst io.Writer, src io.Reader) (written int64, err error) {
    size := 32 * 1024
    buf := make([]byte, size)
res:
    for {
        select {
        case <-ctx.Done():
            break res
        case <-time.After(10 * time.Minute):
            // 设置读写超时
            err = errors.New("Copy write time out")
            break res
        default:
            nr, er := src.Read(buf)
            if nr > 0 {
                nw, ew := dst.Write(buf[0:nr])
                if nw > 0 {
                    written += int64(nw)
                }
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

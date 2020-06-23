package pool

import (
    "context"
    "encoding/binary"
    "github.com/djaigoo/logkit"
    "io"
    "net"
    "os"
    "sync"
    "sync/atomic"
    "syscall"
    "time"
)

const (
    bufSize = 2048
)

type closeWriter interface {
    CloseWrite() error
}

type opErr string

func (oe opErr) Error() string {
    return string(oe)
}

const (
    ErrInterrupt = opErr("interrupt")
    ErrClosed    = opErr("closed")
    ErrVersion   = opErr("version")
    ErrLength    = opErr("length")
    ErrCommand   = opErr("command")
)

func CheckErr(err error) bool {
    if err == nil || err == ErrInterrupt {
        return false
    }
    return true
}

// version
const (
    VER = 1
)

type cmd uint8

func (c cmd) String() string {
    switch c {
    case TransHeartbeat:
        return "TransHeartbeat"
    case Transing:
        return "Transing"
    case TransInterrupt:
        return "TransInterrupt"
    case TransInterruptAck:
        return "TransInterruptAck"
    case TransClose:
        return "TransClose"
    case TransCloseAck:
        return "TransCloseAck"
    case TransCloseWrite:
        return "TransCloseWrite"
    case TransErr:
        return "TransErr"
    default:
        return ""
    }
}

// command
const (
    TransHeartbeat    = cmd(iota)
    Transing          // 1 传输数据
    TransInterrupt    // 2 中断本次数据传输但不断开连接
    TransInterruptAck // 3 中断本次数据传输但不断开连接
    TransClose        // 4 主动关闭
    TransCloseAck     // 5 被动关闭
    TransCloseWrite   // 6 关闭写
    TransErr          = 0x7F
)

type Content struct {
    ver  uint8
    cmd  uint8
    len  uint32
    data []byte
}

type Conn struct {
    conn   net.Conn
    status cmd
    
    readBuf   []byte
    readErr   error
    readMutex sync.Mutex
    
    sendInterruptFlag int32
    
    closed          bool
    heartbeatTicker *time.Ticker
    ctx             context.Context
    cancel          context.CancelFunc
    
    Inited    bool
    pooled    bool
    createdAt time.Time
    usedAt    atomic.Value
    
    readBytes  int64
    writeBytes int64
}

func NewConn(conn net.Conn) *Conn {
    if conn == nil {
        return nil
    }
    c := &Conn{
        conn:            conn,
        heartbeatTicker: time.NewTicker(15 * time.Second),
        createdAt:       time.Now(),
        usedAt:          atomic.Value{},
    }
    c.ctx, c.cancel = context.WithCancel(context.Background())
    c.usedAt.Store(time.Now())
    c.Reset()
    go c.loopRead()
    go c.Heartbeat()
    return c
}

func (c *Conn) loopRead() {
    defer func() {
        c.cancel()
    }()
    for {
        if c.IsClose() {
            return
        }
        c.readErr = c.read()
        if c.readErr != nil {
            if c.readErr == ErrInterrupt {
                continue
            }
            if c.readErr == io.EOF {
                logkit.Debugf("[loopRead] read conn:%s->%s EOF", c.LocalAddr().String(), c.RemoteAddr().String())
                return
            }
            if operr, ok := c.readErr.(*net.OpError); ok {
                if operr.Op == "read" && operr.Err != nil {
                    if syserr, ok := operr.Err.(*os.SyscallError); ok && syserr.Syscall == "read" && syserr.Err == syscall.ETIMEDOUT {
                        logkit.Debugf("[loopRead] read conn:%s->%s ETIMEDOUT", c.LocalAddr().String(), c.RemoteAddr().String())
                        return
                    }
                }
            }
            logkit.Errorf("[loopRead] read conn:%s->%s error %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.readErr.Error())
            return
        }
    }
}

func (c *Conn) read() (err error) {
    // defer logkit.Debugf("read over conn:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if c.IsClose() {
        logkit.Debugf("[read] conn %s->%s is closed EOF", c.LocalAddr().String(), c.RemoteAddr().String())
        return io.EOF
    }
    // TODO read: connect timed out ？
    head := make([]byte, 6)
    n, err := c.conn.Read(head)
    if err != nil {
        logkit.Warnf("[read] conn %s->%s Read error %s", c.LocalAddr().String(), c.RemoteAddr().String(), err.Error())
        return err
    }
    if n < 6 {
        return ErrLength
    }
    if head[0] != VER {
        return ErrVersion
    }
    command := cmd(head[1])
    if command != TransHeartbeat && command != Transing {
        logkit.Warnf("[read] %s from %s -> %s status %s", command, c.conn.RemoteAddr().String(), c.conn.LocalAddr().String(), c.status)
    }
    switch command {
    case TransHeartbeat:
        // keep alive heartbeat
        return nil
    case Transing:
        c.status = Transing
        l := int(binary.BigEndian.Uint32(head[2:6]))
        tl := bufSize
        tmp := make([]byte, bufSize)
        nn := 0
        for nn < l {
            if l-nn < bufSize {
                tl = l - nn
            }
            n, err = c.conn.Read(tmp[:tl])
            if err != nil {
                return err
            }
            c.readMutex.Lock()
            c.readBuf = append(c.readBuf, tmp[:n]...)
            c.readMutex.Unlock()
            nn += n
        }
        return nil
    case TransInterrupt:
        if c.status == TransInterrupt {
            return ErrInterrupt
        }
        c.status = TransInterrupt
        _, err = c.conn.Write([]byte{VER, byte(TransInterruptAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        return ErrInterrupt
    case TransInterruptAck:
        c.status = TransInterruptAck
        return ErrInterrupt
    case TransClose:
        _, err = c.conn.Write([]byte{VER, byte(TransCloseAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        c.close()
        return io.EOF
    case TransCloseAck:
        c.close()
        c.status = TransCloseAck
        return io.EOF
    case TransCloseWrite:
        _, err = c.conn.Write([]byte{VER, byte(TransCloseAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        if wconn, ok := c.conn.(closeWriter); ok {
            return wconn.CloseWrite()
        }
        c.close()
        c.status = TransCloseWrite
        return io.EOF
    default:
        logkit.Errorf("[read] invalid cmd")
        return ErrCommand
    }
}

func (c *Conn) Read(b []byte) (n int, err error) {
    // logkit.Infof("call Read cur:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    for len(c.readBuf) == 0 {
        if c.readErr != nil {
            return 0, c.readErr
        }
        time.Sleep(100 * time.Millisecond)
    }
    
    c.readMutex.Lock()
    defer c.readMutex.Unlock()
    if len(c.readBuf) > len(b) {
        copy(b, c.readBuf[:len(b)])
        c.readBuf = c.readBuf[len(b):]
        return len(b), nil
    } else {
        copy(b[:len(c.readBuf)], c.readBuf)
        n = len(c.readBuf)
        c.readBuf = c.readBuf[len(c.readBuf):]
        return n, nil
    }
}

func (c *Conn) Write(b []byte) (n int, err error) {
    // logkit.Infof("call Write cur:%s->%s  status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if c.IsClose() {
        return 0, ErrClosed
    }
    // 模拟写入已断开连接
    if c.IsInterrupt() {
        return 0, ErrInterrupt
    }
    l := len(b)
    ldata := make([]byte, 4)
    binary.BigEndian.PutUint32(ldata, uint32(l))
    data := make([]byte, 0, l+6)
    data = append(data, VER, byte(Transing))
    data = append(data, ldata...)
    data = append(data, b...)
    n, err = c.conn.Write(data)
    if err != nil {
        return 0, err
    }
    return n - 6, nil
}

func (c *Conn) sendCmd(id cmd) error {
    if id != TransHeartbeat {
        logkit.Warnf("[sendCmd] call %s cur:%s->%s status %s", id, c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    }
    _, err := c.conn.Write([]byte{VER, byte(id), 0, 0, 0, 0})
    return err
}

func (c *Conn) Heartbeat() error {
    defer logkit.Warnf("[Heartbeat] conn %s->%s stop heartbeat", c.LocalAddr().String(), c.RemoteAddr().String())
    defer func() {
        c.closed = true
    }()
    for {
        select {
        case <-c.heartbeatTicker.C:
            err := c.sendCmd(TransHeartbeat)
            if err != nil {
                logkit.Errorf("[Heartbeat] conn %s->%s send heartbeat %s", c.LocalAddr().String(), c.RemoteAddr().String(), err.Error())
                c.readErr = err
                return err
            }
        case <-c.ctx.Done():
            return ErrClosed
        }
    }
}

func (c *Conn) interrupt() error {
    if c.IsInterrupt() {
        return nil
    }
    if c.IsClose() {
        return ErrClosed
    }
    if c.sendInterruptFlag > 0 {
        return nil
    }
    err := c.sendCmd(TransInterrupt)
    if err != nil {
        return err
    }
    atomic.AddInt32(&c.sendInterruptFlag, 1)
    return nil
}

func (c *Conn) Interrupt(timeout time.Duration) (err error) {
    t := time.NewTimer(timeout)
    for !c.IsInterrupt() {
        select {
        case <-t.C:
            c.close()
            return ErrClosed
        default:
            err = c.interrupt()
            if err != nil {
                return err
            }
            time.Sleep(1 * time.Second)
        }
    }
    return nil
}

func (c *Conn) close() error {
    c.closed = true
    c.status = TransClose
    return c.conn.Close()
}

func (c *Conn) CloseWrite() error {
    if c.IsClose() {
        return nil
    }
    c.closed = true
    return c.sendCmd(TransCloseWrite)
}

func (c *Conn) Close() error {
    if c.IsClose() {
        return nil
    }
    c.closed = true
    return c.sendCmd(TransClose)
}

func (c *Conn) LocalAddr() net.Addr {
    return c.conn.LocalAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
    return c.conn.RemoteAddr()
}

func (c *Conn) SetDeadline(t time.Time) error {
    return c.conn.SetDeadline(t)
}

func (c *Conn) SetReadDeadline(t time.Time) error {
    return c.conn.SetReadDeadline(t)
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
    return c.conn.SetWriteDeadline(t)
}

func (c *Conn) UsedAt() time.Time {
    return c.usedAt.Load().(time.Time)
}

func (c *Conn) SetUsedAt(tm time.Time) {
    c.usedAt.Store(tm)
}

func (c *Conn) AddWriteBytes(n int64) {
    atomic.AddInt64(&c.writeBytes, n)
}

func (c *Conn) AddReadBytes(n int64) {
    atomic.AddInt64(&c.readBytes, n)
}

func (c *Conn) Status() cmd {
    return c.status
}

func (c *Conn) IsClose() bool {
    return c.closed || c.status == TransClose || c.status == TransCloseAck || c.status == TransCloseWrite
}

func (c *Conn) IsInterrupt() bool {
    return c.status == TransInterruptAck || c.status == TransInterrupt
}

func (c *Conn) ClearReadBuffer() {
    c.readMutex.Lock()
    defer c.readMutex.Unlock()
    logkit.Alertf("[ClearReadBuffer] cur read buffer len %d", len(c.readBuf))
    c.readBuf = make([]byte, 0, 2048)
}

// 重置连接
func (c *Conn) Reset() {
    // clear old data 清理脏数据
    c.ClearReadBuffer()
    // 重置连接状态
    c.status = TransHeartbeat
    c.readErr = nil
    atomic.StoreInt32(&c.sendInterruptFlag, 0)
}

package pool

import (
    "encoding/binary"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "io"
    "net"
    "sync"
    "sync/atomic"
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
    case TransStart:
        return "TransStart"
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
    TransStart        = cmd(iota)
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
    conn       net.Conn
    status     cmd
    readBuf    []byte
    readErr    error
    readLock   sync.Mutex
    readTicker *time.Ticker
    
    chInterruptAck chan bool
    
    rdLocked bool
    
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
        conn:           conn,
        status:         TransInterrupt,
        readBuf:        make([]byte, 0, 2048),
        readTicker:     time.NewTicker(500 * time.Millisecond),
        chInterruptAck: make(chan bool),
        createdAt:      time.Now(),
        usedAt:         atomic.Value{},
    }
    c.usedAt.Store(time.Now())
    c.Start()
    return c
}

func (c *Conn) read() (err error) {
    if c.status == TransClose || c.status == TransCloseAck || c.status == TransCloseWrite {
        return ErrClosed
    }
    if len(c.readBuf) != 0 {
        // buf full
        return nil
    }
    head := make([]byte, 6)
    n, err := c.conn.Read(head)
    if err != nil {
        logkit.Errorf("[read] head error %s", err.Error())
        return err
    }
    if n < 6 {
        logkit.Errorf("[read] recv len not 6")
        return ErrLength
    }
    if head[0] != VER {
        logkit.Errorf("[read] head ver not 1 %#v", head)
        return ErrVersion
    }
    // logkit.Debugf("read head %#v", head)
    switch cmd(head[1]) {
    case TransStart:
        c.status = TransStart
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
            c.readBuf = append(c.readBuf, tmp[:n]...)
            nn += n
        }
        return nil
    case TransInterrupt:
        logkit.Warnf("[read] TransInterrupt from %s -> %s", c.conn.RemoteAddr().String(), c.conn.LocalAddr().String())
        c.status = TransInterrupt
        _, err = c.conn.Write([]byte{VER, byte(TransInterruptAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        return ErrInterrupt
    case TransInterruptAck:
        logkit.Warnf("[read] TransInterruptAck from %s -> %s", c.conn.RemoteAddr().String(), c.conn.LocalAddr().String())
        c.status = TransInterruptAck
        c.chInterruptAck <- true
        return ErrInterrupt
    case TransClose:
        logkit.Warnf("[read] TransClose from %s -> %s", c.conn.RemoteAddr().String(), c.conn.LocalAddr().String())
        _, err = c.conn.Write([]byte{VER, byte(TransCloseAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        
        if c.status == TransClose || c.status == TransCloseAck || c.status == TransCloseWrite {
            c.readTicker.Stop()
            c.conn.Close()
            return io.EOF
        } else {
            c.status = TransClose
            return nil
        }
    case TransCloseAck:
        logkit.Warnf("[read] TransCloseAck from %s -> %s", c.conn.RemoteAddr().String(), c.conn.LocalAddr().String())
        if c.status == TransClose || c.status == TransCloseAck || c.status == TransCloseWrite {
            c.readTicker.Stop()
            if wconn, ok := c.conn.(closeWriter); ok {
                wconn.CloseWrite()
            } else {
                c.conn.Close()
            }
            return io.EOF
        } else {
            c.status = TransCloseAck
            return nil
        }
    case TransCloseWrite:
        logkit.Warnf("[read] TransCloseWrite from %s -> %s", c.conn.RemoteAddr().String(), c.conn.LocalAddr().String())
        c.status = TransCloseWrite
        _, err = c.conn.Write([]byte{VER, byte(TransCloseAck), 0, 0, 0, 0})
        if err != nil {
            return err
        }
        if wconn, ok := c.conn.(closeWriter); ok {
            return wconn.CloseWrite()
        }
        
        if c.status == TransClose || c.status == TransCloseAck || c.status == TransCloseWrite {
            c.readTicker.Stop()
            c.conn.Close()
            return io.EOF
        } else {
            c.status = TransClose
            return nil
        }
    default:
        logkit.Errorf("[read] invalid cmd")
        return ErrCommand
    }
}

func (c *Conn) Read(b []byte) (n int, err error) {
    // logkit.Infof("call Read cur:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if len(c.readBuf) == 0 {
        err = c.read()
        if err != nil {
            return 0, err
        }
    }
    
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

func (c *Conn) Start() error {
    _, err := c.conn.Write([]byte{VER, byte(TransStart), 0, 0, 0, 0})
    return err
}

func (c *Conn) Interrupt(timeout time.Duration) error {
    logkit.Debugf("[read] call Interrupt cur:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if c.status == TransInterrupt || c.status == TransInterruptAck {
        return nil
    }
    _, err := c.conn.Write([]byte{VER, byte(TransInterrupt), 0, 0, 0, 0})
    if err != nil {
        return err
    }
    if !c.waitInterruptAck(timeout) {
        return errors.New("io timeout")
    }
    return nil
}

func (c *Conn) CloseWrite() error {
    logkit.Debugf("[read] call CloseWrite cur:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if c.status == TransCloseAck {
        return nil
    }
    _, err := c.conn.Write([]byte{VER, byte(TransCloseWrite), 0, 0, 0, 0})
    return err
}

func (c *Conn) Close() error {
    logkit.Debugf("[read] call Close cur:%s->%s status %s", c.LocalAddr().String(), c.RemoteAddr().String(), c.status)
    if c.status == TransCloseAck {
        return nil
    }
    _, err := c.conn.Write([]byte{VER, byte(TransClose), 0, 0, 0, 0})
    return err
}

// WaitInterruptAck block until recv interrupt
func (c *Conn) waitInterruptAck(timeout time.Duration) bool {
    select {
    case <-c.chInterruptAck:
        return true
    case <-time.After(timeout):
        return false
    }
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

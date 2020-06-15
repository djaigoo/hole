package pool

import (
    "crypto/tls"
    "errors"
    "fmt"
    "github.com/djaigoo/logkit"
    "net"
    "sync"
    "sync/atomic"
    "time"
)

var ErrPoolTimeout = errors.New("connection pool timeout")

var timers = sync.Pool{
    New: func() interface{} {
        t := time.NewTimer(time.Hour)
        t.Stop()
        return t
    },
}

// Stats contains pool state information and accumulated stats.
type Stats struct {
    // 从空闲列表中获取成功的次数
    Hits uint32 // number of times free connection was found in the pool
    // 从空闲列表中获取失败次数
    Misses uint32 // number of times free connection was NOT found in the pool
    // 从池中获取连接超时次数
    Timeouts uint32 // number of times a wait timeout occurred
    
    // 连接池总连接数
    TotalConns uint32 // number of total connections in the pool
    // 空闲连接数
    IdleConns uint32 // number of idle connections in the pool
    // 过期连接数
    StaleConns uint32 // number of stale connections removed from the pool
    
    // 移除连接数
    CloseConns uint32
}

func (s *Stats) String() string {
    return fmt.Sprintf("Hits:%d Misses:%d Timeouts:%d TotalConns:%d IdleConns:%d StaleConns:%d CloseConns:%d DiffConns:%d",
        s.Hits, s.Misses, s.Timeouts, s.TotalConns, s.IdleConns, s.StaleConns, s.CloseConns, s.Misses-s.CloseConns-s.TotalConns)
}

type Pooler interface {
    NewConn() (*Conn, error)
    CloseConn(*Conn) error
    
    Get() (*Conn, error)
    Put(*Conn)
    Remove(*Conn) error
    
    Len() int
    IdleLen() int
    Stats() *Stats
    
    Close() error
}

type Options struct {
    Dialer  func() (*Conn, error)
    OnClose func(*Conn) error
    
    PoolSize           int
    MinIdleConns       int
    MaxConnAge         time.Duration
    PoolTimeout        time.Duration
    IdleTimeout        time.Duration
    IdleCheckFrequency time.Duration
}

type ConnPool struct {
    opt *Options
    
    dialErrorsNum uint32 // atomic
    
    lastDialErrorMu sync.RWMutex
    lastDialError   error
    
    queue chan struct{}
    
    connsMu      sync.Mutex
    conns        []*Conn
    idleConns    []*Conn
    poolSize     int
    idleConnsLen int
    
    stats Stats
    
    _closed uint32 // atomic
    
    inSize  int64
    outSize int64
}

var _ Pooler = (*ConnPool)(nil)

func NewConnPool(opt *Options) *ConnPool {
    p := &ConnPool{
        opt: opt,
        
        queue:     make(chan struct{}, opt.PoolSize),
        conns:     make([]*Conn, 0, opt.PoolSize),
        idleConns: make([]*Conn, 0, opt.PoolSize),
    }
    
    for i := 0; i < opt.MinIdleConns; i++ {
        p.checkMinIdleConns()
    }
    
    if opt.IdleTimeout > 0 && opt.IdleCheckFrequency > 0 {
        go p.reaper(opt.IdleCheckFrequency)
    }
    
    return p
}

func (p *ConnPool) checkMinIdleConns() {
    if p.opt.MinIdleConns == 0 {
        return
    }
    if p.poolSize < p.opt.PoolSize && p.idleConnsLen < p.opt.MinIdleConns {
        // 保证有多个空闲连接
        p.poolSize++
        p.idleConnsLen++
        go p.addIdleConn()
    }
}

func (p *ConnPool) addIdleConn() {
    cn, err := p.newConn(true)
    if err != nil {
        return
    }
    // 这个也算miss
    atomic.AddUint32(&p.stats.Misses, 1)
    p.connsMu.Lock()
    p.conns = append(p.conns, cn)
    p.idleConns = append(p.idleConns, cn)
    p.connsMu.Unlock()
}

func (p *ConnPool) NewConn() (*Conn, error) {
    return p._NewConn(false)
}

// _NewConn new conn pooled 表示是否放回池中
func (p *ConnPool) _NewConn(pooled bool) (*Conn, error) {
    cn, err := p.newConn(pooled)
    if err != nil {
        return nil, err
    }
    
    p.connsMu.Lock()
    p.conns = append(p.conns, cn)
    if pooled {
        if p.poolSize < p.opt.PoolSize {
            p.poolSize++
        } else {
            cn.pooled = false
        }
    }
    p.connsMu.Unlock()
    return cn, nil
}

func (p *ConnPool) newConn(pooled bool) (*Conn, error) {
    // 检测连接池是否关闭
    if p.closed() {
        return nil, ErrClosed
    }
    
    if atomic.LoadUint32(&p.dialErrorsNum) >= uint32(p.opt.PoolSize) {
        return nil, p.getLastDialError()
    }
    
    netConn, err := p.opt.Dialer()
    if err != nil {
        p.setLastDialError(err)
        if atomic.AddUint32(&p.dialErrorsNum, 1) == uint32(p.opt.PoolSize) {
            go p.tryDial()
        }
        return nil, err
    }
    
    netConn.pooled = pooled
    return netConn, nil
}

// tryDial 尝试拨号
func (p *ConnPool) tryDial() {
    for {
        if p.closed() {
            return
        }
        
        conn, err := p.opt.Dialer()
        if err != nil {
            p.setLastDialError(err)
            time.Sleep(time.Second)
            continue
        }
        
        atomic.StoreUint32(&p.dialErrorsNum, 0)
        _ = conn.Close()
        return
    }
}

func (p *ConnPool) setLastDialError(err error) {
    p.lastDialErrorMu.Lock()
    p.lastDialError = err
    p.lastDialErrorMu.Unlock()
}

func (p *ConnPool) getLastDialError() error {
    p.lastDialErrorMu.RLock()
    err := p.lastDialError
    p.lastDialErrorMu.RUnlock()
    return err
}

// Get returns existed connection from the pool or creates a new one.
func (p *ConnPool) Get() (*Conn, error) {
    if p.closed() {
        return nil, ErrClosed
    }
    
    err := p.waitTurn()
    if err != nil {
        return nil, err
    }
    
    for {
        p.connsMu.Lock()
        cn := p.popIdle()
        p.connsMu.Unlock()
        
        if cn == nil {
            break
        }
        
        // 检测空闲连接是否过期
        if p.isStaleConn(cn) {
            _ = p.CloseConn(cn)
            continue
        }
        
        atomic.AddUint32(&p.stats.Hits, 1)
        return cn, nil
    }
    
    atomic.AddUint32(&p.stats.Misses, 1)
    
    newcn, err := p._NewConn(true)
    if err != nil {
        p.freeTurn()
        return nil, err
    }
    
    return newcn, nil
}

func (p *ConnPool) getTurn() {
    p.queue <- struct{}{}
}

func (p *ConnPool) waitTurn() error {
    select {
    case p.queue <- struct{}{}:
        return nil
    default:
        timer := timers.Get().(*time.Timer)
        timer.Reset(p.opt.PoolTimeout)
        
        select {
        case p.queue <- struct{}{}:
            if !timer.Stop() {
                <-timer.C
            }
            timers.Put(timer)
            return nil
        case <-timer.C:
            timers.Put(timer)
            atomic.AddUint32(&p.stats.Timeouts, 1)
            return ErrPoolTimeout
        }
    }
}

func (p *ConnPool) freeTurn() {
    <-p.queue
}

// popIdle 返回空闲连接
func (p *ConnPool) popIdle() *Conn {
    if len(p.idleConns) == 0 {
        return nil
    }
    
    idx := len(p.idleConns) - 1
    cn := p.idleConns[idx]
    p.idleConns = p.idleConns[:idx]
    p.idleConnsLen--
    p.checkMinIdleConns()
    return cn
}

// Put 归还连接
func (p *ConnPool) Put(cn *Conn) {
    if !cn.pooled {
        p.Remove(cn)
        return
    }
    
    p.connsMu.Lock()
    p.idleConns = append(p.idleConns, cn)
    p.idleConnsLen++
    p.connsMu.Unlock()
    p.freeTurn()
}

// Remove 从pool中移除cn 并 close
func (p *ConnPool) Remove(cn *Conn) error {
    p.removeConn(cn)
    if cn.pooled {
        p.freeTurn()
    }
    return p.closeConn(cn)
}

func (p *ConnPool) CloseConn(cn *Conn) error {
    p.removeConn(cn)
    return p.closeConn(cn)
}

// removeConn 移除连接池中的某条连接
func (p *ConnPool) removeConn(cn *Conn) {
    p.connsMu.Lock()
    for i, c := range p.conns {
        if c == cn {
            p.conns = append(p.conns[:i], p.conns[i+1:]...)
            if cn.pooled {
                p.poolSize--
                p.checkMinIdleConns()
            }
            break
        }
    }
    p.connsMu.Unlock()
}

// closeConn 执行设定的OnClose函数并关闭连接
func (p *ConnPool) closeConn(cn *Conn) error {
    atomic.AddUint32(&p.stats.CloseConns, 1)
    if p.opt.OnClose != nil {
        _ = p.opt.OnClose(cn)
    }
    atomic.AddInt64(&p.inSize, cn.writeBytes)
    atomic.AddInt64(&p.outSize, cn.readBytes)
    return cn.Close()
}

// Len returns total number of connections.
func (p *ConnPool) Len() int {
    p.connsMu.Lock()
    n := len(p.conns)
    p.connsMu.Unlock()
    return n
}

// IdleLen returns number of idle connections.
func (p *ConnPool) IdleLen() int {
    p.connsMu.Lock()
    n := p.idleConnsLen
    p.connsMu.Unlock()
    return n
}

// Stats 返回连接池状态
func (p *ConnPool) Stats() *Stats {
    idleLen := p.IdleLen()
    return &Stats{
        Hits:     atomic.LoadUint32(&p.stats.Hits),
        Misses:   atomic.LoadUint32(&p.stats.Misses),
        Timeouts: atomic.LoadUint32(&p.stats.Timeouts),
        
        TotalConns: uint32(p.Len()),
        IdleConns:  uint32(idleLen),
        StaleConns: atomic.LoadUint32(&p.stats.StaleConns),
        
        CloseConns: atomic.LoadUint32(&p.stats.CloseConns),
    }
}

// closed 检测pool是否关闭
func (p *ConnPool) closed() bool {
    return atomic.LoadUint32(&p._closed) == 1
}

// Filter 筛选连接并关闭
func (p *ConnPool) Filter(fn func(*Conn) bool) error {
    var firstErr error
    p.connsMu.Lock()
    for _, cn := range p.conns {
        if fn(cn) {
            if err := p.closeConn(cn); err != nil && firstErr == nil {
                firstErr = err
            }
        }
    }
    p.connsMu.Unlock()
    return firstErr
}

// Close 关闭连接池
func (p *ConnPool) Close() error {
    defer func() {
        logkit.Noticef("[Pool] SUM IN %d SUM OUT %d", p.inSize, p.outSize)
    }()
    
    if !atomic.CompareAndSwapUint32(&p._closed, 0, 1) {
        return ErrClosed
    }
    
    var firstErr error
    p.connsMu.Lock()
    for _, cn := range p.conns {
        if err := p.closeConn(cn); err != nil && firstErr == nil {
            firstErr = err
        }
    }
    p.conns = nil
    p.poolSize = 0
    p.idleConns = nil
    p.idleConnsLen = 0
    p.connsMu.Unlock()
    
    return firstErr
}

// reapStaleConn 检测第一个空闲连接是否过期 过期则返回
func (p *ConnPool) reapStaleConn() *Conn {
    if len(p.idleConns) == 0 {
        return nil
    }
    
    cn := p.idleConns[0]
    if !p.isStaleConn(cn) {
        return nil
    }
    
    p.idleConns = append(p.idleConns[:0], p.idleConns[1:]...)
    p.idleConnsLen--
    
    return cn
}

// ReapStaleConns 关闭过期连接
func (p *ConnPool) ReapStaleConns() (int, error) {
    var n int
    for {
        p.getTurn()
        
        p.connsMu.Lock()
        cn := p.reapStaleConn()
        p.connsMu.Unlock()
        
        if cn != nil {
            p.removeConn(cn)
        }
        
        p.freeTurn()
        
        if cn != nil {
            p.closeConn(cn)
            n++
        } else {
            break
        }
    }
    return n, nil
}

// reaper 定时清理过期连接
func (p *ConnPool) reaper(frequency time.Duration) {
    ticker := time.NewTicker(frequency)
    defer ticker.Stop()
    
    for range ticker.C {
        if p.closed() {
            break
        }
        n, err := p.ReapStaleConns()
        if err != nil {
            // internal.Logf("ReapStaleConns failed: %s", err)
            continue
        }
        atomic.AddUint32(&p.stats.StaleConns, uint32(n))
    }
}

// isStaleConn 检测连接是否过期
func (p *ConnPool) isStaleConn(cn *Conn) bool {
    if p.opt.IdleTimeout == 0 && p.opt.MaxConnAge == 0 {
        return false
    }
    
    now := time.Now()
    if p.opt.IdleTimeout > 0 && now.Sub(cn.UsedAt()) >= p.opt.IdleTimeout {
        return true
    }
    if p.opt.MaxConnAge > 0 && now.Sub(cn.createdAt) >= p.opt.MaxConnAge {
        return true
    }
    
    return false
}

var Pool Pooler

func Close() error {
    return Pool.Close()
}

func Start(addr string, size int, config *tls.Config) {
    opt := &Options{
        Dialer: func() (*Conn, error) {
            var conn net.Conn
            var err error
            if config == nil {
                conn, err = net.DialTimeout("tcp", addr, 5*time.Second)
            } else {
                conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 1 * time.Minute}, "tcp", addr, config)
            }
            if err != nil {
                logkit.Errorf("[Pool] Dialer error %s", err.Error())
                return nil, err
            }
            logkit.Alertf("[Pool] New Conn %s", conn.LocalAddr().String())
            return NewConn(conn), nil
        },
        OnClose: func(conn *Conn) error {
            logkit.Alertf("[Pool] OnClose Conn %s, read bytes:%d, write bytes:%d", conn.LocalAddr().String(), conn.readBytes, conn.writeBytes)
            return nil
        },
        PoolSize:           size, // max pool conn nums
        MinIdleConns:       0,
        MaxConnAge:         24 * time.Hour,   // check create time
        PoolTimeout:        5 * time.Second,  // pool get time out
        IdleTimeout:        15 * time.Minute, // check use at time
        IdleCheckFrequency: 30 * time.Second,
    }
    Pool = NewConnPool(opt)
    go func() {
        for range time.NewTicker(15 * time.Second).C {
            logkit.Debugf("[Pool] status: %s", Pool.Stats())
        }
    }()
}

func Get() (conn *Conn, err error) {
    for conn == nil {
        conn, err = Pool.Get()
        if conn != nil {
            if conn.IsClose() {
                logkit.Alertf("[Pool] GET closed conn %s --> %s", conn.LocalAddr().String(), conn.RemoteAddr().String())
                Remove(conn, RClose)
                conn = nil
                continue
            }
            logkit.Debugf("[Pool] GET conn %s", conn.LocalAddr().String())
            conn.Reset()
        }
    }
    return conn, err
}

func Put(conn *Conn) {
    logkit.Debugf("[Pool] PUT conn %s", conn.LocalAddr().String())
    Pool.Put(conn)
}

type Reason uint8

func (r Reason) String() string {
    switch r {
    case RClose:
        return "rClose"
    case RStale:
        return "rStale"
    case RReadErr:
        return "RReadErr"
    case RWriteErr:
        return "RWriteErr"
    }
    return ""
}

const (
    RClose = Reason(iota)
    RStale
    RReadErr
    RWriteErr
)

func Remove(conn *Conn, r Reason) error {
    logkit.Debugf("[Pool] Remove conn %s reason %s", conn.LocalAddr().String(), r)
    return Pool.Remove(conn)
}

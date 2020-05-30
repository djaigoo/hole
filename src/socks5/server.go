package socks5

import (
    "encoding/binary"
    "github.com/djaigoo/logkit"
    "github.com/pkg/errors"
    "io"
    "net"
    "strconv"
)

type Attr struct {
    Command uint8
    Atyp    uint8
    Addr    []byte
    Port    []byte
}

// readAtyp 按照atyp从conn读取addr
func readAtyp(conn net.Conn, atyp uint8) (ret []byte, err error) {
    switch atyp {
    case IPv4:
        ret = make([]byte, 4)
        _, err = io.ReadFull(conn, ret)
    case Domain:
        l := make([]byte, 1)
        _, err = io.ReadFull(conn, l)
        if err != nil {
            break
        }
        ret = make([]byte, l[0])
        _, err = io.ReadFull(conn, ret)
    case IPv6:
        ret = make([]byte, 16)
        _, err = io.ReadFull(conn, ret)
    default:
        err = errors.Wrapf(errors.New(""), "invalid atype %#v", atyp)
    }
    return ret, err
}

// GetAttrByConn 从conn获取attr
func GetAttrByConn(conn net.Conn) (*Attr, error) {
    a := &Attr{}
    tmp := make([]byte, 2)
    _, err := io.ReadFull(conn, tmp)
    if err != nil {
        return nil, err
    }
    a.Command = tmp[0]
    a.Atyp = tmp[1]
    // logkit.Debugf("%#v", tmp)
    a.Addr, err = readAtyp(conn, a.Atyp)
    if err != nil {
        return nil, err
    }
    
    a.Port = make([]byte, 2)
    _, err = io.ReadFull(conn, a.Port)
    if err != nil {
        return nil, err
    }
    return a, nil
}

func (a *Attr) Marshal() ([]byte, error) {
    ret := []byte{a.Command, a.Atyp}
    if a.Atyp == Domain {
        ret = append(ret, uint8(len(a.Addr)))
    }
    ret = append(ret, a.Addr...)
    ret = append(ret, a.Port...)
    return ret, nil
}

func (a *Attr) Unmarshal(data []byte) error {
    if len(data) < 4 {
        return nil
    }
    a.Command = data[0]
    a.Atyp = data[1]
    switch a.Atyp {
    case IPv4:
        a.Addr = data[2:6]
        a.Port = data[6:]
    case Domain:
        l := data[3]
        a.Addr = data[3 : 3+l]
        a.Port = data[3+l:]
    case IPv6:
        a.Addr = data[2:18]
        a.Port = data[18:]
    }
    if len(a.Port) != 2 {
        return nil
    }
    return nil
}

func (a *Attr) step2(conn net.Conn) error {
    tmp := make([]byte, 2)
    _, err := io.ReadFull(conn, tmp)
    if err != nil {
        return err
    }
    if tmp[0] != VER {
        return nil
    }
    if tmp[1] == 0 {
        return nil
    }
    tmp = make([]byte, tmp[1])
    _, err = io.ReadFull(conn, tmp)
    if err != nil {
        return err
    }
    // TODO handle method
    _, err = conn.Write([]byte{VER, NoAuth})
    return err
}

func (a *Attr) step4(conn net.Conn) error {
    tmp := make([]byte, 4)
    _, err := io.ReadFull(conn, tmp)
    if err != nil {
        return err
    }
    if tmp[0] != VER {
        return nil
    }
    a.Command = tmp[1]
    a.Atyp = tmp[3]
    a.Addr, err = readAtyp(conn, a.Atyp)
    if err != nil {
        return err
    }
    
    a.Port = make([]byte, 2)
    _, err = io.ReadFull(conn, a.Port)
    if err != nil {
        return err
    }
    if a.Command == Connect {
        logkit.Warnf("[Handshake] send connect %s --> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
        _, err = conn.Write([]byte{VER, Succeeded, 0, IPv4, 127, 0, 0, 1, 0x04, 0x3e})
    } else {
        logkit.Warnf("[Handshake] send udp %s --> %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
        _, err = conn.Write([]byte{VER, Succeeded, 0, IPv4, 127, 0, 0, 1, 0x04, 0x3e})
    }
    return err
}

func (a *Attr) Handshake(conn net.Conn) error {
    err := a.step2(conn)
    if err != nil {
        return err
    }
    return a.step4(conn)
}

// GetHost
func (a *Attr) GetHost() string {
    host := ""
    switch a.Atyp {
    case IPv4:
        host = net.IP(a.Addr).String()
    case Domain:
        host = string(a.Addr)
    case IPv6:
        host = net.IP(a.Addr).String()
    }
    return host + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(a.Port)))
}

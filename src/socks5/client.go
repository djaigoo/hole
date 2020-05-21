package socks5

import "net"

type Client struct {
    conn net.Conn
    atyp uint8
    addr []byte
}

func (c *Client) Step1() error {
    _, err := c.conn.Write([]byte{VER, 1, NoAuth})
    return err
}

func (c *Client) Step2() {

}

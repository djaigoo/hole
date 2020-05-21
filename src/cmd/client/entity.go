package main

import (
    "github.com/pkg/errors"
    "net"
)

const (
    socksVer5       = 5
    socksCmdConnect = 1
    socksCmdUDP     = 3
)

var (
    errAddrType      = errors.New("socks addr type not supported")
    errVer           = errors.New("socks version not supported")
    errMethod        = errors.New("socks only support 1 method now")
    errAuthExtraData = errors.New("socks authentication get extra data")
    errReqExtraData  = errors.New("socks request get extra data")
    errCmd           = errors.New("socks command not supported")
)

const (
    IdVer   = 0
    IdCmd   = 1
    IdType  = 3 // address type index
    IdIP0   = 4 // ip address start index
    IdDmLen = 4 // domain address length index
    IdDm0   = 5 // domain address start index
    
    TypeIPv4 = 1 // type is ipv4 address
    TypeDm   = 3 // type is domain address
    TypeIPv6 = 4 // type is ipv6 address
    
    LenIPv4   = 3 + 1 + net.IPv4len + 2 // 3(ver+cmd+rsv) + 1addrType + ipv4 + 2port
    LenIPv6   = 3 + 1 + net.IPv6len + 2 // 3(ver+cmd+rsv) + 1addrType + ipv6 + 2port
    LenDmBase = 3 + 1 + 1 + 2           // 3 + 1addrType + 1addrLen + 2port, plus addrLen
)

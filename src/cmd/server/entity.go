package main

import "net"

const (
    AddrMask byte = 0xf
    
    IdType  = 0 // address type index
    IdIP0   = 1 // ip address start index
    IdDmLen = 1 // domain address length index
    IdDm0   = 2 // domain address start index
    
    TypeIPv4 = 1 // type is ipv4 address
    TypeDm   = 3 // type is domain address
    TypeIPv6 = 4 // type is ipv6 address
    
    LenIPv4   = net.IPv4len + 2 // ipv4 + 2port
    LenIPv6   = net.IPv6len + 2 // ipv6 + 2port
    LenDmBase = 2               // 1addrLen + 2port, plus addrLen
    // LenHmacSha1 = 10
)

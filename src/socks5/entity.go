package socks5

// version
const (
    VER = 0x05
)

// method
const (
    NoAuth   = 0x00
    GssAPI   = 0x01
    Auth     = 0x03
    NoAccept = 0xFF
)

// Command
const (
    Connect = 0x01
    Bind    = 0x02
    Udp     = 0x03
)

// Atyp
const (
    IPv4   = 0x1
    Domain = 0x3
    IPv6   = 0x4
)

// rep
const (
    Succeeded               = 0x00 // succeeded
    ServerFailure           = 0x01 // general SOCKS server failure
    NotAllowed              = 0x02 // connection not allowed by ruleset
    NetworkUnreachable      = 0x03 // Network unreachable
    HostUnreachable         = 0x04 // Host unreachable
    ConnectRefused          = 0x05 // Connection refused
    TTLExpired              = 0x06 // TTL expired
    NotSupportedCommand     = 0x07 // Command not supported
    NotSupportedAddressType = 0x08 // Address type not supported
)

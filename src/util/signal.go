package util

import (
    "os"
    "os/signal"
    "syscall"
)

func Signal() os.Signal {
    sign := make(chan os.Signal)
    signal.Notify(sign, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGKILL)
    return <-sign
}

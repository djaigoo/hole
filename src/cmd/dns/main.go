package main

import (
    "github.com/djaigoo/hole/src/utils"
    "github.com/djaigoo/logkit"
)

func main() {
    
    logkit.Infof("dns server quit with signal %d", utils.Signal())
}

#!/usr/bin/env bash

ver=$(go version | awk -F. '{print $2}')
if [[ ${ver} < 13 ]]
then
    echo 'go version need 1.13 and above'
    exit
fi

go env -w GO111MODULE=on
go env -w GOPROXY=https://goproxy.cn,https://goproxy.io,direct

server='hole'
client='hole-client'

goos=$(go env GOOS)
case "${goos}" in
    'windows')
    server='hole.exe'
    client='hole-client.exe'
    ;;
esac

go build -o ${server} src/cmd/server/*
go build -o ${client} src/cmd/client/*

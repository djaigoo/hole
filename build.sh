#!/usr/bin/env bash

go env -w GOPROXY=https://goproxy.cn,direct
go env -w GO111MODULE=on

go build -o hole src/cmd/server/*

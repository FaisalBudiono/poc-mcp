#!/usr/bin/env bash

CGO_ENABLED=0 go build -o server cmd/server/main.go

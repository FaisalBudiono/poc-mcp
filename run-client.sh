#!/usr/bin/env bash

./make-server.sh

go run ./cmd/client/main.go

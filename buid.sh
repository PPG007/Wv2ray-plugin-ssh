#!/bin/bash

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/plugin-ssh-amd64-windows.exe
sha256sum bin/plugin-ssh-amd64-windows.exe | awk '{print $1}' > bin/plugin-ssh-amd64-windows.dgst
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/plugin-ssh-amd64-linux
sha256sum bin/plugin-ssh-amd64-linux | awk '{print $1}' > bin/plugin-ssh-amd64-linux.dgst
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/plugin-ssh-arm64-linux
sha256sum bin/plugin-ssh-arm64-linux | awk '{print $1}' > bin/plugin-ssh-arm64-linux.dgst
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/plugin-ssh-amd64-darwin
sha256sum bin/plugin-ssh-amd64-darwin | awk '{print $1}' > bin/plugin-ssh-amd64-darwin.dgst
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/plugin-ssh-arm64-darwin
sha256sum bin/plugin-ssh-arm64-darwin | awk '{print $1}' > bin/plugin-ssh-arm64-darwin.dgst

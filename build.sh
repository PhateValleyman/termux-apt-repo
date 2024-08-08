#!/data/data/com.termux/files/usr/bin/bash
GOOS=linux GOARCH=arm GOARM=5 go build -v -x -mod=vendor -ldflags '-X "termux-apt-repo/version.Version=1.0"' -o ./bin/termux-apt-repo ./main.go

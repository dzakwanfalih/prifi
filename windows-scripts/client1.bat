@echo off
cd ..\\dissent\\
go run main.go config.go relay.go -client=1 -socks=false
pause
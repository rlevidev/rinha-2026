package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: lb <port> <uds1> [uds2 ...]")
		os.Exit(1)
	}

	port := os.Args[1]
	udsPaths := os.Args[2:]

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("Listen TCP: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Printf("LB listening on :%s, workers: %v\n", port, udsPaths)

	workerIdx := 0
	for {
		conn, err := ln.(*net.TCPListener).AcceptTCP()
		if err != nil {
			fmt.Printf("AcceptTCP: %v\n", err)
			continue
		}

		fd, err := conn.File()
		if err != nil {
			fmt.Printf("Conn File: %v\n", err)
			conn.Close()
			continue
		}
		conn.Close() // Close in LB, worker handles it

		workerPath := udsPaths[workerIdx%len(udsPaths)]
		if err := sendFD(workerPath, int(fd.Fd())); err != nil {
			fmt.Printf("SendFD: %v\n", err)
			unix.Close(int(fd.Fd()))
		}
		workerIdx++
	}
}

func sendFD(udsPath string, fd int) error {
	addr, err := net.ResolveUnixAddr("unix", udsPath)
	if err != nil {
		return err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	fileConn := conn.(*net.UnixConn)
	unixConn, err := fileConn.SyscallConn()
	if err != nil {
		return err
	}

	oob := unix.UnixRights(fd)
	return unixConn.Control(func(s uintptr) {
		err = unix.Sendmsg(int(s), nil, oob, nil, 0)
	})
}

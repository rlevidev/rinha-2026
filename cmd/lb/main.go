package main

import (
	"io"
	"net"
	"os"
	"sync/atomic"
	"time"
)

var counter uint64

func main() {
	if len(os.Args) < 3 {
		panic("usage: lb <port> <socket1> [socket2...]")
	}
	port := os.Args[1]
	workers := os.Args[2:]

	// Wait for workers
	for _, w := range workers {
		for i := 0; i < 30; i++ {
			c, err := net.Dial("unix", w)
			if err == nil {
				c.Close()
				break
			}
			time.Sleep(time.Second)
		}
	}

	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		panic(err)
	}

	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go handle(c, workers)
	}
}

func handle(client net.Conn, workers []string) {
	defer client.Close()

	idx := atomic.AddUint64(&counter, 1) % uint64(len(workers))
	worker, err := net.Dial("unix", workers[idx])
	if err != nil {
		return
	}
	defer worker.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(worker, client); done <- struct{}{} }()
	go func() { io.Copy(client, worker); done <- struct{}{} }()
	<-done
}

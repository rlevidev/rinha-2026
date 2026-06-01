package main

import (
	"errors"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"

	lbrelay "github.com/rlevidev/rinha-2026/internal/lb"
)

var sockets = []string{
	"/sockets/api1.sock",
	"/sockets/api2.sock",
}

var rr atomic.Uint64

func waitForSocket(path string) bool {
	for i := 0; i < 300; i++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func dialBackend(seq uint64) (net.Conn, string, error) {
	primary := lbrelay.PickBackend(seq, sockets)
	secondary := lbrelay.PickBackend(seq+1, sockets)

	for _, socket := range []string{primary, secondary} {
		conn, err := net.DialTimeout("unix", socket, 2*time.Second)
		if err == nil {
			return conn, socket, nil
		}
		log.Printf("backend %s failed: %v", socket, err)
	}

	return nil, "", errors.New("all backends unavailable")
}

func relay(client net.Conn, backend net.Conn) {
	errc := make(chan error, 2)

	go func() {
		_, err := io.Copy(backend, client)
		errc <- err
	}()

	go func() {
		_, err := io.Copy(client, backend)
		errc <- err
	}()

	err := <-errc
	if err != nil && !errors.Is(err, io.EOF) {
		log.Printf("relay ended: %v", err)
	}

	_ = client.Close()
	_ = backend.Close()
	<-errc
}

func serveConn(client net.Conn) {
	defer client.Close()

	seq := rr.Add(1) - 1
	backend, socket, err := dialBackend(seq)
	if err != nil {
		log.Printf("unable to route client: %v", err)
		return
	}
	log.Printf("routing client to %s", socket)
	relay(client, backend)
}

func main() {
	for _, socket := range sockets {
		if !waitForSocket(socket) {
			log.Printf("socket %s did not become ready in time; continuing", socket)
		}
	}

	listener, err := net.Listen("tcp", ":9999")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	log.Println("lb listening on :9999")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept failed: %v", err)
			continue
		}

		go serveConn(conn)
	}
}

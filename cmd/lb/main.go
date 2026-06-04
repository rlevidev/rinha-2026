package main

import (
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <tcp_port> <uds_path1> [uds_path2 ...]", os.Args[0])
	}

	tcpPort := os.Args[1]
	udsPaths := os.Args[2:]

	// 1. Go Runtime Tuning
	runtimeTuning()

	// 2. Connect to worker Unix sockets
	workerUDs := make([]*unix.SockaddrUnix, len(udsPaths))
	for i, path := range udsPaths {
		workerUDs[i] = &unix.SockaddrUnix{Name: path}
	}

	workerConns := make([]int, len(udsPaths)) // Connected worker UDS FDs

	// Retry connecting to workers for 30 seconds
	log.Println("Connecting to worker Unix sockets...")
	for attempt := 0; attempt < 30; attempt++ {
		allConnected := true
		for i, udsAddr := range workerUDs {
			if workerConns[i] == 0 { // Not yet connected
				fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
				if err != nil {
					log.Printf("Failed to create worker UDS socket for %s: %v", udsAddr.Name, err)
					allConnected = false
					continue
				}
				if err := unix.Connect(fd, udsAddr); err != nil {
					log.Printf("Failed to connect to worker UDS %s: %v", udsAddr.Name, err)
					unix.Close(fd)
					allConnected = false
					continue
				}
				log.Printf("Connected to worker UDS %s", udsAddr.Name)
				workerConns[i] = fd
			}
		}
		if allConnected {
			break
		}
		time.Sleep(1 * time.Second)
		if attempt == 29 {
			log.Fatalf("Failed to connect to all workers after 30 retries.")
		}
	}

	defer func() {
		for _, fd := range workerConns {
			if fd > 0 {
				unix.Close(fd)
			}
		}
	}()

	// 3. Listen on TCP port
	tcpAddr, err := net.ResolveTCPAddr("tcp", ":"+tcpPort)
	if err != nil {
		log.Fatalf("Failed to resolve TCP address: %v", err)
	}
	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Fatalf("Failed to listen on TCP port %s: %v", tcpPort, err)
	}
	defer listener.Close()

	log.Printf("Listening on TCP port %s", tcpPort)

	workerIdx := 0
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Printf("Failed to accept TCP connection: %v", err)
			continue
		}

		// Get raw FD from TCP connection
		rawConn, err := conn.SyscallConn()
		if err != nil {
			log.Printf("Failed to get raw connection: %v", err)
			conn.Close()
			continue
		}

		var clientFD int
		rawConn.Control(func(fd uintptr) {
			clientFD = int(fd)
		})

		// Set aggressive socket options on clientFD BEFORE passing
		setSocketOptions(clientFD)

		// Pass FD to worker via SCM_RIGHTS (round-robin)
		targetWorkerFD := workerConns[workerIdx]
		workerIdx = (workerIdx + 1) % len(workerConns)

		// Send clientFD to worker using SCM_RIGHTS
		err = sendClientFD(targetWorkerFD, clientFD)
		if err != nil {
			log.Printf("Failed to send client FD to worker %d: %v", targetWorkerFD, err)
			unix.Close(clientFD) // Close if unable to pass
		}
	}
}

// runtimeTuning applies Go runtime optimizations
func runtimeTuning() {
}

// sendClientFD sends a file descriptor over a Unix domain socket
func sendClientFD(udsFD, clientFD int) error {
	msg := unix.UnixRights(clientFD)
	// We send an empty message but with the control message carrying the FD
	return unix.Sendmsg(udsFD, nil, msg, nil, 0)
}

// setSocketOptions applies aggressive socket options to the client FD
func setSocketOptions(fd int) {
	// TCP_NODELAY: disable Nagle's algorithm
	unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_NODELAY, 1)
	// TCP_QUICKACK: disable delayed ACKs
	unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_QUICKACK, 1)
	// SO_REUSEADDR, SO_REUSEPORT
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}

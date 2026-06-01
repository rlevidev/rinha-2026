package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"
)

var sockets = []string{
	"/sockets/api1.sock",
	"/sockets/api2.sock",
}

var rr atomic.Uint64

func unixTransport(socketPath string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     30 * time.Second,
	}
}

func proxyFor(socketPath string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "unix"
		},
		Transport: unixTransport(socketPath),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("backend %s failed: %v", socketPath, err)
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		},
	}
}

func waitForSocket(path string) {
	for i := 0; i < 300; i++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func main() {
	proxies := [2]*httputil.ReverseProxy{
		proxyFor(sockets[0]),
		proxyFor(sockets[1]),
	}

	for _, socket := range sockets {
		waitForSocket(socket)
	}

	srv := &http.Server{
		Addr:              ":9999",
		ReadHeaderTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			idx := int(rr.Add(1)-1) & 1
			proxies[idx].ServeHTTP(w, r)
		}),
	}

	log.Println("lb listening on :9999")
	log.Fatal(srv.ListenAndServe())
}

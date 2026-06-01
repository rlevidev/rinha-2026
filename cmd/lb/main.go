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

var counter atomic.Uint64

// newUnixTransport cria um http.Transport que roteia todas as requisições
// para um único Unix socket, ignorando o host da URL.
//
// Por que Transport por socket e não um único Transport compartilhado:
//   - Cada Transport mantém seu próprio pool de conexões idle por host-key.
//   - Se usássemos um Transport só, as conexões para api1 e api2 misturariam
//     no mesmo pool sob a mesma host-key ("unix"), quebrando o round-robin.
//   - Com um Transport por socket, o pool é dedicado: até MaxIdleConns
//     conexões keep-alive ficam abertas para cada backend, prontas para
//     reutilização sem overhead de handshake por request.
//
// MaxIdleConns=512 e IdleConnTimeout=90s:
//   - Com 500 req/s e p99 < 1ms, cada conexão serve ~1000 req/s.
//   - 512 conexões idle cobrem picos sem criar novas conexões sob carga.
func newUnixTransport(socketPath string) http.RoundTripper {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// Ignora network e address vindos da URL sempre conecta no socket fixo.
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 512,
		IdleConnTimeout:     90 * time.Second,
		// DisableKeepAlives: false (padrão) keep-alive ativo
	}
}

func main() {
	// Um ReverseProxy dedicado por backend.
	// O proxy reescreve a URL para "http://unix/...", o Transport ignora o host
	// e conecta sempre no socketPath correto via DialContext.
	proxies := [2]*httputil.ReverseProxy{
		newProxy(sockets[0], sockets[1]), // Passando fallback para falhas
		newProxy(sockets[1], sockets[0]),
	}

	srv := &http.Server{
		Addr: ":9999",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Round-robin atômico: distribui requisições igualmente entre api1 e api2.
			n := counter.Add(1) - 1
			idx := n % 2

			proxies[idx].ServeHTTP(w, r)
		}),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	log.Println("LB escutando em :9999")
	log.Fatal(srv.ListenAndServe())
}

// newProxy cria um ReverseProxy apontando para socketPath.
// Recebe um fallback para redirecionar em caso de 502/falha.
func newProxy(socketPath string, fallbackSocket string) *httputil.ReverseProxy {
	transport := newUnixTransport(socketPath)
	fallbackTransport := newUnixTransport(fallbackSocket)

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "unix"
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("LB: erro no backend %s, tentando fallback %s: %v", socketPath, fallbackSocket, err)

			// Em caso de falha no principal, o LB não desiste (Error HTTP) e sim injeta
			// a request no Transport do Fallback.
			req := r.Clone(r.Context())

			// Setup mock proxy fallback manual
			fbProxy := &httputil.ReverseProxy{
				Director: func(req *http.Request) {
					req.URL.Scheme = "http"
					req.URL.Host = "unix"
				},
				Transport: fallbackTransport,
				ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
					log.Printf("LB: Erro crítico em ambos os backends. %v", err)
					http.Error(w, "backend unavailable", http.StatusBadGateway)
				},
			}
			fbProxy.ServeHTTP(w, req)
		},
	}

	return proxy
}

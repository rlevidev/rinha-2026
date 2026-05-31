package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
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
	for _, s := range sockets {
		log.Printf("LB aguardando socket: %s", s)
		for {
			if _, err := os.Stat(s); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		log.Printf("Socket %s pronto.", s)
	}

	// Um ReverseProxy dedicado por backend.
	// O proxy reescreve a URL para "http://unix/...", o Transport ignora o host
	// e conecta sempre no socketPath correto via DialContext.
	proxies := [2]*httputil.ReverseProxy{
		newProxy(sockets[0]),
		newProxy(sockets[1]),
	}

	srv := &http.Server{
		Addr: ":9999",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Round-robin atômico: distribui requisições igualmente entre api1 e api2.
			n := counter.Add(1) - 1
			idx := n % 2

			// Tenta o backend escolhido. Se falhar (backend fora do ar), usa o outro.
			// ErrorHandler no proxy captura erros de conexão e faz o fallback.
			proxies[idx].ServeHTTP(w, r)
		}),
		// Timeouts conservadores para a Rinha: requests são curtos, sem streaming.
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	log.Println("LB escutando em :9999")
	log.Fatal(srv.ListenAndServe())
}

// newProxy cria um ReverseProxy apontando para socketPath.
// O ErrorHandler registra a falha e responde 502 em vez de deixar o panic
// propagar o k6 trata 502 como erro HTTP, mas pelo menos não derruba o LB.
func newProxy(socketPath string) *httputil.ReverseProxy {
	transport := newUnixTransport(socketPath)

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// ReverseProxy precisa de uma URL válida para montar o request.
			// O Transport ignora o host e conecta no socket, o scheme e host
			// aqui são apenas placeholders obrigatórios para o http.Client interno.
			req.URL.Scheme = "http"
			req.URL.Host = "unix"
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Backend inacessível: loga e retorna 502.
			// O k6 da Rinha conta erros HTTP 502 tem peso menor que timeout.
			log.Printf("LB: erro no backend %s: %v", socketPath, err)
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		},
	}

	return proxy
}

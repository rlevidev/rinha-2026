package httpx

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/rlevidev/rinha-2026/internal/fraud"
)

const (
	fraudPath = "/fraud-score"
	readyPath = "/ready"
)

var errBadRequest = errors.New("bad request")

// ServeConn reads one framed HTTP request from conn and writes the matching
// pre-rendered response bytes. Parse or scoring failures fall back to a 200
// response instead of surfacing a 5xx.
func ServeConn(conn net.Conn, h *fraud.Handler) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	resp := h.Fallback()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving request: %v", rec)
				resp = h.Fallback()
			}
		}()

		if next, err := handleRequest(br, h); err == nil {
			resp = next
		}
	}()

	_, _ = writeAll(conn, resp)
}

func handleRequest(r *bufio.Reader, h *fraud.Handler) ([]byte, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	method, path, ok := parseRequestLine(line)
	if !ok {
		return nil, errBadRequest
	}

	contentLength := -1
	for {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if line == "" {
			break
		}
		name, value, ok := splitHeader(line)
		if !ok {
			return nil, errBadRequest
		}
		if strings.EqualFold(name, "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, errBadRequest
			}
			contentLength = n
		}
	}

	switch {
	case method == "GET" && path == readyPath:
		return h.Ready(), nil
	case method == "POST" && path == fraudPath:
		if contentLength < 0 {
			return nil, errBadRequest
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		return h.Score(body), nil
	default:
		return nil, errBadRequest
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func parseRequestLine(line string) (method, path string, ok bool) {
	firstSpace := strings.IndexByte(line, ' ')
	if firstSpace <= 0 {
		return "", "", false
	}
	secondSpace := strings.IndexByte(line[firstSpace+1:], ' ')
	if secondSpace <= 0 {
		return "", "", false
	}
	secondSpace += firstSpace + 1
	method = line[:firstSpace]
	path = line[firstSpace+1 : secondSpace]
	if method == "" || path == "" {
		return "", "", false
	}
	return method, path, true
}

func splitHeader(line string) (name, value string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", false
	}
	return line[:colon], line[colon+1:], true
}

func writeAll(w io.Writer, p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n, err := w.Write(p)
		total += n
		if n == 0 && err == nil {
			return total, io.ErrShortWrite
		}
		p = p[n:]
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

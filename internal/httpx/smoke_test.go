package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rlevidev/rinha-2026/internal/fraud"
	"github.com/rlevidev/rinha-2026/internal/index"
)

func startTestAPI(t *testing.T, socketPath, indexDir string) {
	t.Helper()

	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	_ = os.Chmod(socketPath, 0o666)
	t.Cleanup(func() {
		ln.Close()
		os.Remove(socketPath)
	})

	set, err := index.LoadSet(indexDir)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	norm, err := fraud.LoadNormalizer("../../resources/normalization.json", "../../resources/mcc_risk.json")
	if err != nil {
		t.Fatalf("load normalizer: %v", err)
	}
	h := &fraud.Handler{Indexes: set, Normalizer: norm}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go ServeConn(conn, h)
		}
	}()

	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("unix", socketPath, 50*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("api on %s never became ready", socketPath)
}

func TestSmokeReady(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api1.sock")
	startTestAPI(t, sock, "../../index")

	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	c.Write([]byte("GET /ready HTTP/1.1\r\n\r\n"))
	resp, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Contains(resp, []byte("200 OK")) {
		t.Fatalf("ready response = %q, want 200 OK", resp)
	}
}

func TestSmokeFraudScorePerPayload(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api1.sock")
	startTestAPI(t, sock, "../../index")

	raw, err := os.ReadFile("../../resources/example-payloads.json")
	if err != nil {
		t.Fatalf("read payloads: %v", err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no example payloads")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", sock, time.Second)
		},
		DisableKeepAlives: true,
	}
	client.Transport = transport

	statuses := make([]int, len(entries))
	bodies := make([]string, len(entries))
	for i, e := range entries {
		req, err := http.NewRequest("POST", "http://unix/fraud-score", bytes.NewReader(e))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("do %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		statuses[i] = resp.StatusCode
		bodies[i] = string(body)
	}

	for i, s := range statuses {
		if s != 200 {
			t.Errorf("payload %d: status=%d body=%s", i, s, bodies[i])
		}
		if !bytes.Contains([]byte(bodies[i]), []byte(`"approved"`)) {
			t.Errorf("payload %d: missing approved in body=%s", i, bodies[i])
		}
	}
}

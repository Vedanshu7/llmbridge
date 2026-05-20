package caching

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// fakeRedis is a minimal in-process Redis server speaking RESP2.
// It supports: PING, GET, SET (with EX), DEL, FLUSHDB.
type fakeRedis struct {
	mu   sync.Mutex
	data map[string]string
	ln   net.Listener
}

func startFakeRedis(t *testing.T) (*fakeRedis, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake redis listen: %v", err)
	}
	fr := &fakeRedis{data: make(map[string]string), ln: ln}
	go fr.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return fr, ln.Addr().String()
}

func (fr *fakeRedis) serve() {
	for {
		conn, err := fr.ln.Accept()
		if err != nil {
			return
		}
		go fr.handleConn(conn)
	}
}

func (fr *fakeRedis) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		args, err := readRESP(r)
		if err != nil {
			return
		}
		reply := fr.dispatch(args)
		if _, err := fmt.Fprint(conn, reply); err != nil {
			return
		}
	}
}

func readRESP(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("expected array, got %q", line)
	}
	var n int
	_, _ = fmt.Sscanf(line[1:], "%d", &n)
	args := make([]string, n)
	for i := range n {
		// $<len>
		hdr, _ := r.ReadString('\n')
		hdr = strings.TrimRight(hdr, "\r\n")
		var slen int
		_, _ = fmt.Sscanf(hdr[1:], "%d", &slen)
		buf := make([]byte, slen+2) // +2 for \r\n
		_, _ = r.Read(buf)
		args[i] = string(buf[:slen])
	}
	return args, nil
}

func (fr *fakeRedis) dispatch(args []string) string {
	if len(args) == 0 {
		return "-ERR empty\r\n"
	}
	cmd := strings.ToUpper(args[0])
	fr.mu.Lock()
	defer fr.mu.Unlock()
	switch cmd {
	case "PING":
		return "+PONG\r\n"
	case "GET":
		if len(args) < 2 {
			return "-ERR wrong arity\r\n"
		}
		v, ok := fr.data[args[1]]
		if !ok {
			return "$-1\r\n"
		}
		return fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
	case "SET":
		if len(args) < 3 {
			return "-ERR wrong arity\r\n"
		}
		fr.data[args[1]] = args[2]
		return "+OK\r\n"
	case "DEL":
		if len(args) < 2 {
			return "-ERR wrong arity\r\n"
		}
		count := 0
		for _, k := range args[1:] {
			if _, ok := fr.data[k]; ok {
				delete(fr.data, k)
				count++
			}
		}
		return fmt.Sprintf(":%d\r\n", count)
	case "FLUSHDB":
		fr.data = make(map[string]string)
		return "+OK\r\n"
	default:
		return "-ERR unknown command\r\n"
	}
}

func (fr *fakeRedis) get(key string) (string, bool) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	v, ok := fr.data[key]
	return v, ok
}

// ---- Tests ----

func TestRedisNewCacheConnectFailure(t *testing.T) {
	_, err := NewRedisCache("127.0.0.1:1") // port 1 should always fail
	if err == nil {
		t.Error("expected connection error for invalid address")
	}
}

func TestRedisPing(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}
	if err := c.Ping(); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestRedisSetAndGet(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	resp := &types.Response{Content: "cached answer", Model: "gpt-4o"}
	c.Set("key1", resp, time.Minute)

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("Get returned false after Set")
	}
	if got.Content != "cached answer" {
		t.Errorf("content = %q", got.Content)
	}
}

func TestRedisGetMissing(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	_, ok := c.Get("no-such-key")
	if ok {
		t.Error("Get should return false for missing key")
	}
}

func TestRedisDelete(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	resp := &types.Response{Content: "to-delete"}
	c.Set("delkey", resp, time.Minute)
	c.Delete("delkey")

	_, ok := c.Get("delkey")
	if ok {
		t.Error("key should be gone after Delete")
	}
}

func TestRedisFlush(t *testing.T) {
	fr, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	resp := &types.Response{Content: "will-be-flushed"}
	c.Set("k1", resp, time.Minute)
	c.Set("k2", resp, time.Minute)
	c.Flush()

	fr.mu.Lock()
	n := len(fr.data)
	fr.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 keys after Flush, got %d", n)
	}
}

func TestRedisSetWithZeroTTL(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	// TTL=0 should still persist (no EX option).
	resp := &types.Response{Content: "no-ttl"}
	c.Set("nottl", resp, 0)

	got, ok := c.Get("nottl")
	if !ok || got.Content != "no-ttl" {
		t.Errorf("expected to get key with zero TTL, ok=%v", ok)
	}
}

func TestRedisGetInvalidJSON(t *testing.T) {
	fr, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	// Manually inject invalid JSON.
	fr.mu.Lock()
	fr.data["badkey"] = "not-json"
	fr.mu.Unlock()

	_, ok := c.Get("badkey")
	if ok {
		t.Error("Get should return false for invalid JSON value")
	}
}

func TestRedisWithPassword(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	// Auth-aware server: expects AUTH command first.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		// Expect AUTH command.
		args, err := readRESP(r)
		if err != nil || len(args) < 2 || strings.ToUpper(args[0]) != "AUTH" {
			_, _ = fmt.Fprint(conn, "-ERR expected AUTH\r\n")
			return
		}
		if args[1] != "secret" {
			_, _ = fmt.Fprint(conn, "-ERR invalid password\r\n")
			return
		}
		_, _ = fmt.Fprint(conn, "+OK\r\n")
		// Handle subsequent PING.
		for {
			args, err := readRESP(r)
			if err != nil {
				return
			}
			if strings.ToUpper(args[0]) == "PING" {
				fmt.Fprint(conn, "+PONG\r\n")
			}
		}
	}()

	addr := ln.Addr().String()
	c, err := NewRedisCacheWithPassword(addr, "secret")
	if err != nil {
		t.Fatalf("NewRedisCacheWithPassword: %v", err)
	}
	if err := c.Ping(); err != nil {
		t.Errorf("Ping after auth: %v", err)
	}
}

func TestRedisRoundTripJSON(t *testing.T) {
	_, addr := startFakeRedis(t)
	c, err := NewRedisCache(addr)
	if err != nil {
		t.Fatal(err)
	}

	resp := &types.Response{
		Content:  "answer",
		Model:    "claude-3",
		Provider: "anthropic",
		Usage:    &types.UsageData{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	c.Set("full-resp", resp, time.Minute)

	got, ok := c.Get("full-resp")
	if !ok {
		t.Fatal("Get returned false")
	}
	b1, _ := json.Marshal(resp)
	b2, _ := json.Marshal(got)
	if string(b1) != string(b2) {
		t.Errorf("round-trip mismatch:\n got  %s\n want %s", b2, b1)
	}
}

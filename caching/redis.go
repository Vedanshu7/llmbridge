package caching

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Vedanshu7/llmbridge/types"
)

// RedisCache stores responses in Redis using the RESP2 wire protocol.
// No external dependencies — uses only stdlib net.Conn.
//
// Only GET, SET (with EX), and DEL commands are used.
// A single persistent TCP connection is reused; it reconnects automatically.
type RedisCache struct {
	addr     string
	password string
	mu       sync.Mutex // serialise all RESP commands on the single conn
	conn     net.Conn
	reader   *bufio.Reader
}

// NewRedisCache returns a RedisCache connected to addr (e.g. "localhost:6379").
func NewRedisCache(addr string) (*RedisCache, error) {
	return NewRedisCacheWithPassword(addr, "")
}

// NewRedisCacheWithPassword returns a RedisCache that authenticates with password.
func NewRedisCacheWithPassword(addr, password string) (*RedisCache, error) {
	c := &RedisCache{addr: addr, password: password}
	if err := c.connect(); err != nil {
		return nil, fmt.Errorf("redis: connect %s: %w", addr, err)
	}
	return c, nil
}

func (c *RedisCache) connect() error {
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return err
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	if c.password != "" {
		if err := c.sendCommand("AUTH", c.password); err != nil {
			_ = conn.Close()
			return fmt.Errorf("AUTH failed: %w", err)
		}
		if _, err := c.readReply(); err != nil {
			return fmt.Errorf("AUTH reply: %w", err)
		}
	}
	return nil
}

func (c *RedisCache) reconnect() error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return c.connect()
}

// Get implements Cache.
func (c *RedisCache) Get(key string) (*types.Response, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	val, err := c.do("GET", key)
	if err != nil || val == nil {
		return nil, false
	}
	s, ok := val.(string)
	if !ok {
		return nil, false
	}
	var resp types.Response
	if json.Unmarshal([]byte(s), &resp) != nil {
		return nil, false
	}
	return &resp, true
}

// Set implements Cache.
func (c *RedisCache) Set(key string, resp *types.Response, ttl time.Duration) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ttl > 0 {
		secs := int(ttl.Seconds())
		if secs < 1 {
			secs = 1
		}
		_, _ = c.do("SET", key, string(raw), "EX", strconv.Itoa(secs))
	} else {
		_, _ = c.do("SET", key, string(raw))
	}
}

// Delete implements Cache.
func (c *RedisCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.do("DEL", key)
}

// Flush removes all keys that could have been set by this cache.
// Note: this calls FLUSHDB which clears the entire Redis database.
// Use with care in shared Redis instances.
func (c *RedisCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.do("FLUSHDB")
}

// Ping verifies the connection is alive.
func (c *RedisCache) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.do("PING")
	if err != nil {
		return err
	}
	if s, _ := v.(string); s != "PONG" {
		return fmt.Errorf("redis: unexpected PING response: %v", v)
	}
	return nil
}

// do executes a command, reconnecting once on network failure.
func (c *RedisCache) do(args ...string) (interface{}, error) {
	if err := c.sendCommand(args...); err != nil {
		if err2 := c.reconnect(); err2 != nil {
			return nil, err2
		}
		if err := c.sendCommand(args...); err != nil {
			return nil, err
		}
	}
	return c.readReply()
}

// sendCommand writes a RESP2 array command to the connection.
func (c *RedisCache) sendCommand(args ...string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&sb, "$%d\r\n%s\r\n", len(a), a)
	}
	_, err := fmt.Fprint(c.conn, sb.String())
	return err
}

// readReply reads and parses one RESP2 reply from the connection.
func (c *RedisCache) readReply() (interface{}, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return nil, fmt.Errorf("redis: empty reply")
	}
	switch line[0] {
	case '+': // Simple string
		return line[1:], nil
	case '-': // Error
		return nil, fmt.Errorf("redis: %s", line[1:])
	case ':': // Integer
		n, err := strconv.ParseInt(line[1:], 10, 64)
		return n, err
	case '$': // Bulk string
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil // nil bulk string
		}
		buf := make([]byte, n+2) // +2 for \r\n
		if _, err := c.reader.Read(buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*': // Array
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil
		}
		arr := make([]interface{}, n)
		for i := range n {
			arr[i], err = c.readReply()
			if err != nil {
				return nil, err
			}
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("redis: unknown reply type %q", line[0])
	}
}

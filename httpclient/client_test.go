package httpclient

import (
	"testing"
	"time"
)

func TestDefaultTransportPoolConfig(t *testing.T) {
	c := New()

	if c.transport.MaxIdleConns != defaultMaxIdleConns {
		t.Fatalf("MaxIdleConns=%d want=%d", c.transport.MaxIdleConns, defaultMaxIdleConns)
	}
	if c.transport.IdleConnTimeout != defaultIdleConnTimeout {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, defaultIdleConnTimeout)
	}
	if c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should default to false")
	}
}

func TestNewWithConfigAppliesTransportPoolConfig(t *testing.T) {
	c := NewWithConfig(Config{
		MaxIdleConns:      17,
		IdleConnTimeout:   42 * time.Second,
		DisableKeepAlives: true,
	})

	if c.transport.MaxIdleConns != 17 {
		t.Fatalf("MaxIdleConns=%d want=17", c.transport.MaxIdleConns)
	}
	if c.transport.IdleConnTimeout != 42*time.Second {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, 42*time.Second)
	}
	if !c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should be true")
	}
}

func TestRebuildTransportKeepsPoolConfig(t *testing.T) {
	c := NewWithConfig(Config{
		MaxIdleConns:      17,
		IdleConnTimeout:   42 * time.Second,
		DisableKeepAlives: true,
	})

	c.SetSocks5Proxy("127.0.0.1:1080")

	if c.transport.MaxIdleConns != 17 {
		t.Fatalf("MaxIdleConns=%d want=17", c.transport.MaxIdleConns)
	}
	if c.transport.IdleConnTimeout != 42*time.Second {
		t.Fatalf("IdleConnTimeout=%s want=%s", c.transport.IdleConnTimeout, 42*time.Second)
	}
	if !c.transport.DisableKeepAlives {
		t.Fatal("DisableKeepAlives should remain true after rebuild")
	}
}

package httpclient

import (
	"testing"
	"time"
)

func TestNewDefaultsAndPool(t *testing.T) {
	c := New(0)
	if c == nil || c.Timeout != DefaultTimeout {
		t.Fatalf("New(0) = %+v", c)
	}
	if c.Transport == nil {
		t.Fatal("New() transport = nil")
	}
	if c := New(5 * time.Second); c.Timeout != 5*time.Second {
		t.Fatalf("New(5s).Timeout = %v", c.Timeout)
	}
}

package tools

import (
	"context"
	"testing"
)

func TestCheckPublicURL(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		url string
		ok  bool
	}{
		{"http://127.0.0.1/x", false},
		{"http://localhost/x", false},
		{"http://192.168.1.1/x", false},
		{"http://10.0.0.1/x", false},
		{"file:///etc/passwd", false},
		{"http://[::1]/x", false},
		{"https://example.com/x", true},
		{"not-a-url", false},
	}
	for _, tc := range cases {
		err := CheckPublicURL(ctx, tc.url)
		if tc.ok && err != nil {
			t.Fatalf("CheckPublicURL(%q) = %v, want ok", tc.url, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("CheckPublicURL(%q) = nil, want error", tc.url)
		}
	}
}

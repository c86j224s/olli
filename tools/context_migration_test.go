package tools

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestReadURLContentWithCanceledContextMakesNoRequest(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})
	ctx := context.WithValue(context.Background(), webTransportContextKey{}, http.RoundTripper(transport))
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ReadURLContentWithContext(ctx, "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected canceled request error")
	}
	if calls != 0 {
		t.Fatalf("canceled request reached transport %d times", calls)
	}
}

func TestCanceledGrepFallbackReturnsContextError(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(root, "file", string(rune('a'+i%10))+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("needle\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handleActionGrep(ctx, "needle", root, root); err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

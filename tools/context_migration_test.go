package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestReadURLContentWithCanceledContextMakesNoRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadURLContentWithContext(ctx, server.URL); err == nil {
		t.Fatal("expected canceled request error")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("canceled request reached server %d times", got)
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

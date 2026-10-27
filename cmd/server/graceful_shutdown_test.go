package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func startTestServer(t *testing.T, handler http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			t.Logf("serve error: %v", err)
		}
	}()
	return srv, "http://" + ln.Addr().String()
}

// TestGracefulShutdownInFlightRequest memverifikasi bahwa srv.Shutdown
// menunggu request yang sedang berjalan selesai sebelum server berhenti.
func TestGracefulShutdownInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		close(done)
	})

	srv, url := startTestServer(t, handler)
	defer srv.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := http.Get(url)
		if err != nil {
			t.Errorf("request failed: %v", err)
			return
		}
		resp.Body.Close()
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- srv.Shutdown(ctx)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete")
	}

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("shutdown returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete")
	}

	wg.Wait()
}

// TestGracefulShutdownTimeout memverifikasi bahwa Shutdown mengembalikan
// error jika request melebihi timeout.
func TestGracefulShutdownTimeout(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	srv, url := startTestServer(t, handler)
	defer srv.Close()

	go func() {
		resp, _ := http.Get(url)
		if resp != nil {
			resp.Body.Close()
		}
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

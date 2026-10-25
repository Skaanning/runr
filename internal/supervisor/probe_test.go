package supervisor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Skaanning/runr/internal/config"
)

func TestGetOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/down":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	for path, want := range map[string]bool{"/ok": true, "/redirect": true, "/down": false, "/missing": false} {
		if got := getOK(context.Background(), srv.URL+path); got != want {
			t.Errorf("getOK(%s) = %v, want %v", path, got, want)
		}
	}
	srv.Close()
	if getOK(context.Background(), srv.URL+"/ok") {
		t.Error("getOK on a closed server = true")
	}
}

func TestDialOK(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !dialOK(context.Background(), addr) {
		t.Errorf("dialOK(%s) = false while listening", addr)
	}
	ln.Close()
	if dialOK(context.Background(), addr) {
		t.Errorf("dialOK(%s) = true after closing", addr)
	}
}

func TestNewProbe(t *testing.T) {
	for _, tc := range []struct {
		p    config.Project
		desc string
	}{
		{config.Project{ReadyPort: 5432}, "port 5432"},
		{config.Project{ReadyURL: "http://localhost:8080/health"}, "http://localhost:8080/health"},
	} {
		if pr := newProbe(&tc.p); pr == nil || pr.desc != tc.desc {
			t.Errorf("newProbe(%+v) = %+v, want desc %q", tc.p, pr, tc.desc)
		}
	}
	for _, p := range []config.Project{{}, {ReadyLog: "up"}, {Ready: config.ReadyExit}} {
		if pr := newProbe(&p); pr != nil {
			t.Errorf("newProbe(%+v) = %+v, want nil", p, pr)
		}
	}
}

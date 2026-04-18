package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// newUpstream starts an httptest server backed by a real local Server.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "fzf/linux-amd64", "fzf-bin-from-upstream")

	s := &Server{Root: root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)
	return httptest.NewServer(mux)
}

func newForwarder(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Upstream: u, Proxy: newProxy(u)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)
	return httptest.NewServer(mux)
}

func TestProxyGetToolPassesThrough(t *testing.T) {
	up := newUpstream(t)
	defer up.Close()
	fw := newForwarder(t, up.URL)
	defer fw.Close()

	resp, body := get(t, fw.URL+"/get_tool?name=fzf&os=linux&arch=amd64")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if body != "fzf-bin-from-upstream" {
		t.Errorf("body %q", body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="linux-amd64"`) {
		t.Errorf("content-disposition missing from proxied response: %q", cd)
	}
}

func TestProxyHeaderPolicy(t *testing.T) {
	// Capture what upstream sees.
	var mu sync.Mutex
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	fw := newForwarder(t, upstream.URL)
	defer fw.Close()

	resp, _ := get(t, fw.URL+"/get_tool?name=whatever")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen.Get("X-Forwarded-For") == "" {
		t.Error("expected X-Forwarded-For to be set by forwarder")
	}
	if h := seen.Get("X-Forwarded-Host"); h != "" {
		t.Errorf("X-Forwarded-Host should not be set, got %q", h)
	}
	if h := seen.Get("X-Forwarded-Proto"); h != "" {
		t.Errorf("X-Forwarded-Proto should not be set, got %q", h)
	}
}

func TestProxyInstallScriptPointsAtForwarder(t *testing.T) {
	up := newUpstream(t)
	defer up.Close()
	fw := newForwarder(t, up.URL)
	defer fw.Close()

	// Even for a package only the upstream has, forwarder should generate
	// a script (it skips the existence check in upstream mode).
	resp, body := get(t, fw.URL+"/install_tool?name=nope")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body=%q", resp.StatusCode, body)
	}
	// BASE should be the forwarder's own host, not the upstream's.
	if strings.Contains(body, up.URL) {
		t.Errorf("script BASE points to upstream %s; got body:\n%s", up.URL, body)
	}
	if !strings.Contains(body, `BASE="`+fw.URL+`"`) {
		t.Errorf("script BASE should be forwarder %s; got body:\n%s", fw.URL, body)
	}
}

func TestProxyInstallScriptRealRun(t *testing.T) {
	// The generated script should work through the forwarder: the script's
	// BASE is the forwarder; it calls /get_tool which is proxied to upstream.
	up := newUpstream(t)
	defer up.Close()
	fw := newForwarder(t, up.URL)
	defer fw.Close()

	// Simulate what the script would do: HEAD then GET via forwarder.
	req, _ := http.NewRequest("HEAD", fw.URL+"/get_tool?name=fzf&os=linux&arch=amd64", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HEAD status %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("HEAD missing Content-Disposition")
	}

	resp, err = http.Get(fw.URL + "/get_tool?name=fzf&os=linux&arch=amd64")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "fzf-bin-from-upstream" {
		t.Errorf("got %q", body)
	}
}

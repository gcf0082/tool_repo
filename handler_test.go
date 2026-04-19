package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "ripgrep/14.0.3/linux-amd64/ripgrep.tar.gz", "rg-14.0.3-linux")
	writeFile(t, root, "ripgrep/14.1.0/linux-amd64/ripgrep.tar.gz", "rg-14.1.0-linux")
	writeFile(t, root, "ripgrep/14.1.0/darwin-arm64/ripgrep.tar.gz", "rg-14.1.0-darwin")
	writeFile(t, root, "fzf/0.1.0/linux-amd64/fzf", "fzf-bin")

	s := &Server{Root: root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)
	mux.HandleFunc("GET /install_tool_cli", s.handleInstallCLI)
	return httptest.NewServer(mux), root
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestGetRipgrepLatest(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp, body := get(t, ts.URL+"/get_tool?name=ripgrep&os=linux&arch=amd64")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, body=%q", resp.StatusCode, body)
	}
	if body != "rg-14.1.0-linux" {
		t.Errorf("got body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("content-type %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="ripgrep.tar.gz"`) {
		t.Errorf("content-disposition %q", cd)
	}
}

func TestGetRipgrepPinnedVersion(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, body := get(t, ts.URL+"/get_tool?name=ripgrep&os=linux&arch=amd64&version=14.0.3")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if body != "rg-14.0.3-linux" {
		t.Errorf("got %q", body)
	}
}

func TestGetRipgrepMissingPlatform(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/get_tool?name=ripgrep&os=linux&arch=arm64")
	if resp.StatusCode != 404 {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestGetFzfVersionless(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, body := get(t, ts.URL+"/get_tool?name=fzf&os=linux&arch=amd64")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if body != "fzf-bin" {
		t.Errorf("got %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type %q", ct)
	}
}

func TestGetUnknownPackage(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/get_tool?name=nope")
	if resp.StatusCode != 404 {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestGetBadName(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/get_tool?name=..%2Fetc")
	if resp.StatusCode != 400 {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetOsWithoutArch(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/get_tool?name=fzf&os=linux")
	if resp.StatusCode != 400 {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestGetMissingOSArch(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	// name only, no os/arch → now 400 (platform-agnostic layout was removed)
	resp, _ := get(t, ts.URL+"/get_tool?name=fzf")
	if resp.StatusCode != 400 {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestHelpPerEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	cases := []struct {
		path   string
		needle string
	}{
		{"/", "Endpoints:"},
		{"/get_tool", "Download a package"},
		{"/install_tool", "Return a shell script"},
	}
	for _, c := range cases {
		resp, body := get(t, ts.URL+c.path)
		if resp.StatusCode != 200 {
			t.Errorf("%s: status %d", c.path, resp.StatusCode)
		}
		if !strings.Contains(body, c.needle) {
			t.Errorf("%s: help body missing %q; got:\n%s", c.path, c.needle, body)
		}
	}
}

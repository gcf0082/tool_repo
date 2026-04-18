package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newScriptServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	scriptsRoot := filepath.Join(root, "scripts")
	if err := os.MkdirAll(filepath.Join(scriptsRoot, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, scriptsRoot, "hello.sh", "#!/bin/sh\necho hello $@\n")
	writeFile(t, scriptsRoot, "deploy/staging.sh", "#!/bin/sh\necho staging $@\n")

	s := &Server{Root: root, ScriptsRoot: scriptsRoot}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_script", s.handleGetScript)
	mux.HandleFunc("PUT /put_script", s.handlePutScript)
	return httptest.NewServer(mux), scriptsRoot
}

func TestGetScriptHit(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	resp, body := get(t, ts.URL+"/get_script?path=hello.sh")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body=%q", resp.StatusCode, body)
	}
	if !strings.Contains(body, "echo hello") {
		t.Errorf("body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Errorf("content-type %q", ct)
	}
}

func TestGetScriptNested(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	resp, body := get(t, ts.URL+"/get_script?path=deploy/staging.sh")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "echo staging") {
		t.Errorf("body %q", body)
	}
}

func TestGetScriptHelp(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	resp, body := get(t, ts.URL+"/get_script")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "GET /get_script") {
		t.Errorf("help body wrong: %q", body)
	}
}

func TestGetScriptBadPath(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	cases := []string{
		"/get_script?path=../etc/passwd",
		"/get_script?path=/abs",
		"/get_script?path=",
	}
	for _, u := range cases {
		resp, _ := get(t, ts.URL+u)
		if resp.StatusCode != 400 {
			t.Errorf("%s: want 400, got %d", u, resp.StatusCode)
		}
	}
}

func TestGetScriptNotFound(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/get_script?path=nope.sh")
	if resp.StatusCode != 404 {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
	// Directory-as-path → 404
	resp, _ = get(t, ts.URL+"/get_script?path=deploy")
	if resp.StatusCode != 404 {
		t.Errorf("dir want 404, got %d", resp.StatusCode)
	}
}

func TestPutScriptCreateAndOverwrite(t *testing.T) {
	ts, root := newScriptServer(t)
	defer ts.Close()

	putURL := ts.URL + "/put_script?path=uploaded/x.sh"
	body1 := "#!/bin/sh\necho first\n"
	req, _ := http.NewRequest("PUT", putURL, strings.NewReader(body1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("first PUT: want 201, got %d", resp.StatusCode)
	}

	// Verify file on disk
	data, err := os.ReadFile(filepath.Join(root, "uploaded", "x.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body1 {
		t.Errorf("file content %q", data)
	}

	// Overwrite
	body2 := "#!/bin/sh\necho second\n"
	req2, _ := http.NewRequest("PUT", putURL, strings.NewReader(body2))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("overwrite PUT: want 200, got %d", resp2.StatusCode)
	}
	data2, _ := os.ReadFile(filepath.Join(root, "uploaded", "x.sh"))
	if string(data2) != body2 {
		t.Errorf("overwritten content %q", data2)
	}
}

func TestPutScriptBadPath(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	req, _ := http.NewRequest("PUT", ts.URL+"/put_script?path=../evil", strings.NewReader("x"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestPutScriptHelp(t *testing.T) {
	ts, _ := newScriptServer(t)
	defer ts.Close()
	// No query → help (GET on /put_script endpoint would not match PUT route,
	// so we hit it with a PUT and no query)
	req, _ := http.NewRequest("PUT", ts.URL+"/put_script", strings.NewReader(""))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("help status %d", resp.StatusCode)
	}
}

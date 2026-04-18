package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInstallScriptRendering(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp, body := get(t, ts.URL+"/install_tool?name=fzf")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body=%q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Errorf("content-type %q", ct)
	}
	for _, want := range []string{
		`NAME="fzf"`,
		`BASE="http://`,
		"/get_tool?name=",
		"curl -fSL",
		"uname -s",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("script missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestInstallUnknownPackage(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/install_tool?name=nope")
	if resp.StatusCode != 404 {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestInstallMissingName(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	// Empty query → help (200). A query with empty name → 400.
	resp, _ := get(t, ts.URL+"/install_tool?name=")
	if resp.StatusCode != 400 {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestPublicBaseURL(t *testing.T) {
	cases := []struct {
		host, xfProto, xfHost string
		tls                   bool
		want                  string
	}{
		{host: "h:1", want: "http://h:1"},
		{host: "h", xfProto: "https", want: "https://h"},
		{host: "h", xfHost: "public.example", want: "http://public.example"},
		{host: "h", xfProto: "https", xfHost: "public.example", want: "https://public.example"},
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "http://"+c.host+"/", nil)
		r.Host = c.host
		if c.xfProto != "" {
			r.Header.Set("X-Forwarded-Proto", c.xfProto)
		}
		if c.xfHost != "" {
			r.Header.Set("X-Forwarded-Host", c.xfHost)
		}
		got := publicBaseURL(r)
		if got != c.want {
			t.Errorf("host=%q xfp=%q xfh=%q: got %q want %q", c.host, c.xfProto, c.xfHost, got, c.want)
		}
	}
}

func TestInstallToolCLIEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp, body := get(t, ts.URL+"/install_tool_cli")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body=%q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Errorf("content-type %q", ct)
	}
	for _, want := range []string{
		"/usr/local/bin/tool_cli",   // default DEST
		"DEST:-",                    // env override pattern
		"cat > \"$tmp\" <<'__TOOL_CLI_EOF__'",
		"#!/usr/bin/env bash",       // start of embedded tool_cli
		"tool_cli — client wrapper", // a marker inside tool_cli
		`"$DEST" set-url "http://`,  // wrapper invokes set-url
		"install -m 755",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install_tool_cli script missing %q", want)
		}
	}
}

func TestInstallToolCLIHelp(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp, body := get(t, ts.URL+"/install_tool_cli?help")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("content-type %q (want text/plain)", ct)
	}
	for _, want := range []string{
		"GET /install_tool_cli",
		"DEST",
		"/usr/local/bin/tool_cli",
		"set-url",
		"sudo",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install_tool_cli help missing %q", want)
		}
	}
	// Ensure we're not accidentally returning the script
	if strings.Contains(body, "__TOOL_CLI_EOF__") {
		t.Error("help response leaked the script heredoc marker")
	}
}

func TestInstallScriptUsesForwardedHost(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/install_tool?name=fzf", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "pkgs.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `BASE="https://pkgs.example.com"`) {
		t.Errorf("expected BASE from forwarded headers; got:\n%s", body)
	}
}

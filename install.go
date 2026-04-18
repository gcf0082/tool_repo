package main

import (
	_ "embed"
	"net/http"
	"text/template"
)

//go:embed install.sh.tmpl
var installTmpl string

const rootHelp = `tool_repo — simple tool distribution

Endpoints:
  /get_tool      — download a package           (curl /get_tool      for details)
  /install_tool  — one-line install script      (curl /install_tool  for details)
`

const getHelp = `GET /get_tool?name=<n>[&os=<os>&arch=<arch>][&version=<v>]

Download a package.

Parameters:
  name     (required) package name
  os       (optional) linux | darwin | windows
  arch     (optional) amd64 | arm64 | 386 | arm
  version  (optional) exact version; omitted = latest

Notes:
  - os and arch must be given together, or neither.
  - Without os/arch, only platform-agnostic artifacts are considered.
  - Response carries Content-Disposition with the original filename.

Examples:
  curl 'http://HOST/get_tool?name=ripgrep&os=linux&arch=amd64' -OJ
  curl 'http://HOST/get_tool?name=mytool' -OJ
`

const installHelp = `GET /install_tool?name=<n>

Return a shell script that detects this host's os/arch via uname and
downloads the package into the current directory. Pipe to 'sh' to run.

Parameters:
  name  (required) package name

Notes:
  - Installs into $PWD; does not touch ~/.local/bin or /usr/local/bin.
  - Archives (.tar.gz/.zip/...) are downloaded but not extracted.

Example:
  curl -fsSL 'http://HOST/install_tool?name=ripgrep' | sh
`

var installTemplate = template.Must(template.New("install").Parse(installTmpl))

func writeHelp(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}

func (s *Server) handleRootHelp(w http.ResponseWriter, r *http.Request) {
	writeHelp(w, rootHelp)
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) == 0 {
		writeHelp(w, installHelp)
		return
	}
	name := r.URL.Query().Get("name")
	if !nameRe.MatchString(name) {
		http.Error(w, "bad or missing name", http.StatusBadRequest)
		return
	}
	if _, err := scan(s.Root, name); err != nil {
		writeErr(w, err)
		return
	}
	base := publicBaseURL(r)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_ = installTemplate.Execute(w, struct {
		Name    string
		BaseURL string
	}{name, base})
}

func publicBaseURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

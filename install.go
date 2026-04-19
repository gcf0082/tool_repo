package main

import (
	"embed"
	_ "embed"
	"net/http"
	"text/template"
)

//go:embed install.sh.tmpl
var installTmpl string

//go:embed install_tool_cli.sh.tmpl
var installCLITmpl string

//go:embed tool_cli_help.txt
var toolCLIHelp string

// tool_cli binaries for every supported platform are baked into the
// server binary at build time. dist.sh / test.sh are responsible for
// populating tool_cli_bin/<os-arch>/tool_cli[.exe] with real binaries
// before running `go build`; otherwise the embedded placeholders are
// empty and /tool_cli serves zero bytes.
//
//go:embed tool_cli_bin
var toolCLIBinaries embed.FS

const rootHelp = `tool_repo — simple tool distribution

Endpoints:
  /get_tool          — download a package             (curl /get_tool          for details)
  /install_tool      — one-line install script        (curl /install_tool      for details)
  /install_tool_cli  — bootstrap the tool_cli client  (curl | sh to install)
  /tool_cli_help     — tool_cli subcommand reference (always-fresh help)
  /get_script        — read a shell script from scripts/<path>
  /put_script        — upload a shell script to scripts/<path>  (PUT, curl -T)

Quick start (as a new client):
  curl -fsSL http://HOST/install_tool_cli | sh
  tool_cli ping
  tool_cli install <name>
`

const getHelp = `GET /get_tool?name=<n>&os=<os>&arch=<arch>[&version=<v>]

Download a package.

Parameters:
  name     (required) package name
  os       (required) linux | darwin | windows
  arch     (required) amd64 | arm64 | 386 | arm
  version  (optional) exact version; omitted = latest (semver max,
                      lexical fallback for non-semver names)

Layout expected on disk:
  packages/<name>/<version>/<os>-<arch>/<file>

Response:
  Content-Disposition: attachment; filename="<file>"
  (use curl -OJ to save with that name)

Examples (raw curl):
  curl 'http://HOST/get_tool?name=ripgrep&os=linux&arch=amd64' -OJ
  curl 'http://HOST/get_tool?name=ripgrep&os=linux&arch=amd64&version=14.0.3' -OJ

Or using the tool_cli client (os/arch auto-detected from uname):
  tool_cli get ripgrep
  tool_cli get ripgrep --version 14.1.0
`

const installHelp = `GET /install_tool?name=<n>

Return a shell script that detects this host's os/arch via uname and
downloads the named package into the current directory. Pipe to 'sh'
to run.

Parameters:
  name  (required) package name

Notes:
  - Installs into $PWD; does not touch ~/.local/bin or /usr/local/bin.
  - Archives (.tar.gz/.zip/...) are downloaded but not extracted.
  - For installing the tool_cli client itself, use /install_tool_cli
    (which defaults to /usr/local/bin/tool_cli and auto-configures
    the server URL).

Example:
  curl -fsSL 'http://HOST/install_tool?name=ripgrep' | sh
`

var installTemplate = template.Must(template.New("install").Parse(installTmpl))
var installCLITemplate = template.Must(template.New("install_cli").Parse(installCLITmpl))

func writeHelp(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}

func (s *Server) handleRootHelp(w http.ResponseWriter, r *http.Request) {
	writeHelp(w, rootHelp)
}

func (s *Server) handleToolCLIHelp(w http.ResponseWriter, r *http.Request) {
	writeHelp(w, toolCLIHelp)
}

// handleToolCLIBin serves the embedded tool_cli binary for the
// requested os+arch. Used by the /install_tool_cli bootstrap so that
// a fresh machine can get the client with just one HTTP round trip.
func (s *Server) handleToolCLIBin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	osName := q.Get("os")
	arch := q.Get("arch")
	if osName == "" || arch == "" {
		http.Error(w, "os and arch are required", http.StatusBadRequest)
		return
	}
	if !allowedOS[osName] || !allowedArch[arch] {
		http.Error(w, "unsupported os/arch", http.StatusBadRequest)
		return
	}
	fname := "tool_cli"
	if osName == "windows" {
		fname = "tool_cli.exe"
	}
	p := "tool_cli_bin/" + osName + "-" + arch + "/" + fname
	data, err := toolCLIBinaries.ReadFile(p)
	if err != nil {
		http.Error(w, "tool_cli binary not bundled for "+osName+"-"+arch, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	_, _ = w.Write(data)
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
	if s.Proxy == nil {
		if _, err := scan(s.Root, name); err != nil {
			writeErr(w, err)
			return
		}
	}
	base := publicBaseURL(r)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_ = installTemplate.Execute(w, struct {
		Name    string
		BaseURL string
	}{name, base})
}

func (s *Server) handleInstallCLI(w http.ResponseWriter, r *http.Request) {
	base := publicBaseURL(r)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_ = installCLITemplate.Execute(w, struct {
		BaseURL string
	}{base})
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

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var pathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

const maxScriptBytes = 16 << 20 // 16 MiB

const getScriptHelp = `GET /get_script?path=<path>

Return the raw content of scripts/<path> (Content-Type: application/x-shellscript).

Parameters:
  path  (required) path under the scripts/ root. Segments must match
                   [A-Za-z0-9._/-]+. '..' is rejected.

Example:
  curl 'http://HOST/get_script?path=hello.sh'

To execute a remote script through the tool_cli client:
  tool_cli run remote://hello.sh arg1 arg2
`

const putScriptHelp = `PUT /put_script?path=<path>

Upload a script to scripts/<path>. Body is the raw script content.
Parent directories are created automatically; existing files are
overwritten. Max body: 16 MiB.

Parameters:
  path  (required) path under the scripts/ root. Segments must match
                   [A-Za-z0-9._/-]+. '..' is rejected.

Response:
  201 Created  — new file
  200 OK       — overwrote existing file
  400          — bad path
  413          — body larger than 16 MiB

Examples:
  curl -T ./local.sh 'http://HOST/put_script?path=deploy/run.sh'
  tool_cli push ./local.sh remote://deploy/run.sh

SECURITY: this endpoint is NOT authenticated. Anyone who can reach
it can cause any 'tool_cli run' caller to execute arbitrary shell.
Deploy only on trusted networks until token auth is added.
`

func (s *Server) resolveScriptPath(p string) (string, error) {
	if p == "" || !pathRe.MatchString(p) || strings.Contains(p, "..") || strings.HasPrefix(p, "/") {
		return "", ErrInvalid
	}
	if s.ScriptsRoot == "" {
		return "", ErrNotFound
	}
	rootAbs, err := filepath.Abs(s.ScriptsRoot)
	if err != nil {
		return "", err
	}
	absClean := filepath.Clean(filepath.Join(rootAbs, p))
	if !strings.HasPrefix(absClean, rootAbs+string(filepath.Separator)) {
		return "", ErrInvalid
	}
	return absClean, nil
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) == 0 {
		writeHelp(w, getScriptHelp)
		return
	}
	if s.Proxy != nil {
		s.Proxy.ServeHTTP(w, r)
		return
	}
	absClean, err := s.resolveScriptPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(absClean)
	if err != nil || st.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-shellscript; charset=utf-8")
	http.ServeFile(w, r, absClean)
}

func (s *Server) handlePutScript(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) == 0 {
		writeHelp(w, putScriptHelp)
		return
	}
	if s.Proxy != nil {
		s.Proxy.ServeHTTP(w, r)
		return
	}
	p := r.URL.Query().Get("path")
	absClean, err := s.resolveScriptPath(p)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(absClean), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxScriptBytes)
	defer r.Body.Close()

	existed := false
	if _, err := os.Stat(absClean); err == nil {
		existed = true
	}

	f, err := os.OpenFile(absClean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(f, r.Body); err != nil {
		f.Close()
		// MaxBytesReader sets status via http.MaxBytesError mapping; try to be explicit
		if maxErr := (*http.MaxBytesError)(nil); err == maxErr || strings.Contains(err.Error(), "http: request body too large") {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := f.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if existed {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	fmt.Fprintf(w, "wrote %s\n", p)
}

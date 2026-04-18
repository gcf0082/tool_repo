package main

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
)

type Server struct {
	Root     string
	Upstream *url.URL
	Proxy    *httputil.ReverseProxy
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.RawQuery) == 0 {
		writeHelp(w, getHelp)
		return
	}
	if s.Proxy != nil {
		s.Proxy.ServeHTTP(w, r)
		return
	}
	q := r.URL.Query()
	name := q.Get("name")
	if !nameRe.MatchString(name) {
		http.Error(w, "bad or missing name", http.StatusBadRequest)
		return
	}
	osName := q.Get("os")
	arch := q.Get("arch")
	version := q.Get("version")

	arts, err := scan(s.Root, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	art, err := resolve(arts, osName, arch, version)
	if err != nil {
		writeErr(w, err)
		return
	}

	abs := filepath.Join(s.Root, art.Path)
	absClean, err := filepath.Abs(abs)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rootAbs, err := filepath.Abs(s.Root)
	if err != nil || !strings.HasPrefix(absClean, rootAbs+string(filepath.Separator)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if ct := contentType(art.Ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, filepath.Base(art.Path)))
	http.ServeFile(w, r, absClean)
}

func contentType(ext string) string {
	switch ext {
	case "":
		return "application/octet-stream"
	case ".tar.gz", ".tgz":
		return "application/gzip"
	case ".tar.bz2", ".tbz2":
		return "application/x-bzip2"
	case ".tar.xz", ".txz":
		return "application/x-xz"
	case ".zip":
		return "application/zip"
	case ".7z":
		return "application/x-7z-compressed"
	default:
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
		return "application/octet-stream"
	}
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrAmbiguous):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

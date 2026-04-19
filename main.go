package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	dir := flag.String("dir", ".", "data root; must contain a packages/ and/or scripts/ subdirectory (ignored when -upstream is set)")
	host := flag.String("host", "0.0.0.0", "bind IP address")
	port := flag.Int("port", 28080, "listen port")
	upstream := flag.String("upstream", "", "if set, proxy business requests to this URL and serve no local data")
	flag.Parse()

	if *port < 1 || *port > 65535 {
		log.Fatalf("port %d out of range", *port)
	}

	s := &Server{}
	if *upstream != "" {
		u, err := url.Parse(*upstream)
		if err != nil || u.Scheme == "" || u.Host == "" {
			log.Fatalf("bad -upstream URL %q: %v", *upstream, err)
		}
		s.Upstream = u
		s.Proxy = newProxy(u)
		log.Printf("upstream mode: proxying /get_tool to %s", u)
	} else {
		if st, err := os.Stat(*dir); err != nil || !st.IsDir() {
			log.Fatalf("-dir %q: not a directory", *dir)
		}
		pkgDir := filepath.Join(*dir, "packages")
		scrDir := filepath.Join(*dir, "scripts")
		if st, err := os.Stat(pkgDir); err == nil && st.IsDir() {
			s.Root = pkgDir
		}
		if st, err := os.Stat(scrDir); err == nil && st.IsDir() {
			s.ScriptsRoot = scrDir
		}
		if s.Root == "" && s.ScriptsRoot == "" {
			log.Fatalf("-dir %q: neither packages/ nor scripts/ found under it", *dir)
		}
		log.Printf("local mode: data root=%s (packages=%v scripts=%v)",
			*dir, s.Root != "", s.ScriptsRoot != "")
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)
	mux.HandleFunc("GET /install_tool_cli", s.handleInstallCLI)
	mux.HandleFunc("GET /tool_cli", s.handleToolCLIBin)
	mux.HandleFunc("GET /tool_cli_help", s.handleToolCLIHelp)
	mux.HandleFunc("GET /get_script", s.handleGetScript)
	mux.HandleFunc("PUT /put_script", s.handlePutScript)

	log.Printf("tool_repo listening on %s", addr)
	if err := http.ListenAndServe(addr, accessLog(mux)); err != nil {
		log.Fatal(fmt.Errorf("listen: %w", err))
	}
}

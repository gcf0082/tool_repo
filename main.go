package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

func main() {
	dir := flag.String("dir", "./packages", "root directory holding package tree (ignored when -upstream is set)")
	host := flag.String("host", "0.0.0.0", "bind IP address")
	port := flag.Int("port", 8080, "listen port")
	upstream := flag.String("upstream", "", "if set, proxy /get_tool to this URL and serve no local packages")
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
		if _, err := os.Stat(*dir); err != nil {
			log.Fatalf("dir %q: %v", *dir, err)
		}
		s.Root = *dir
		log.Printf("local mode: serving %s", *dir)
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)
	mux.HandleFunc("GET /install_tool_cli", s.handleInstallCLI)

	log.Printf("tool_repo listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(fmt.Errorf("listen: %w", err))
	}
}

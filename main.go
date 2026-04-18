package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
)

func main() {
	dir := flag.String("dir", "./packages", "root directory holding package tree")
	host := flag.String("host", "0.0.0.0", "bind IP address")
	port := flag.Int("port", 8080, "listen port")
	flag.Parse()

	if _, err := os.Stat(*dir); err != nil {
		log.Fatalf("dir %q: %v", *dir, err)
	}
	if *port < 1 || *port > 65535 {
		log.Fatalf("port %d out of range", *port)
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	s := &Server{Root: *dir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRootHelp)
	mux.HandleFunc("GET /get_tool", s.handleGet)
	mux.HandleFunc("GET /install_tool", s.handleInstall)

	log.Printf("tool_repo serving %s on %s", *dir, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(fmt.Errorf("listen: %w", err))
	}
}

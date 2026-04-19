package main

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func newProxy(upstream *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			if clientIP, _, err := net.SplitHostPort(r.In.RemoteAddr); err == nil {
				if prior := r.In.Header.Get("X-Forwarded-For"); prior != "" {
					clientIP = prior + ", " + clientIP
				}
				r.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			r.Out.Header.Del("X-Forwarded-Host")
			r.Out.Header.Del("X-Forwarded-Proto")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[ERROR] proxy %s %s -> %s: %v", r.Method, r.URL.Path, upstream, err)
			http.Error(w, "upstream unavailable: "+err.Error(), http.StatusBadGateway)
		},
	}
}

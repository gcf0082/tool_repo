package main

import (
	"net"
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
	}
}

package main

import (
	"log"
	"net"
	"net/http"
	"time"
)

// logResponseWriter captures status and byte count for the access log.
type logResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (l *logResponseWriter) WriteHeader(s int) {
	if l.status == 0 {
		l.status = s
	}
	l.ResponseWriter.WriteHeader(s)
}

func (l *logResponseWriter) Write(b []byte) (int, error) {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	n, err := l.ResponseWriter.Write(b)
	l.bytes += n
	return n, err
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		// First hop = original client
		if i := len(xf); i > 0 {
			// Take first comma-separated entry
			for j := 0; j < i; j++ {
				if xf[j] == ',' {
					return xf[:j]
				}
			}
			return xf
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// accessLog is a middleware that prints one line per request.
func accessLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logResponseWriter{ResponseWriter: w}
		h.ServeHTTP(lw, r)
		status := lw.status
		if status == 0 {
			status = http.StatusOK
		}
		tag := "INFO"
		if status >= 500 {
			tag = "ERROR"
		} else if status >= 400 {
			tag = "WARN"
		}
		ua := r.Header.Get("User-Agent")
		if len(ua) > 60 {
			ua = ua[:60] + "..."
		}
		log.Printf("[%s] %s %s %q -> %d %dB %s ua=%q",
			tag, clientIP(r), r.Method, r.RequestURI, status, lw.bytes,
			time.Since(start).Round(time.Microsecond), ua)
	})
}

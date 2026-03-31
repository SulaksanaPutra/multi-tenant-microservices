package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed public/*
var content embed.FS

var serverSessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	publicFS, err := fs.Sub(content, "public")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Proxy Mailpit API requests internally over Docker network to bypass CORS
	http.HandleFunc("/api/mailpit/", func(w http.ResponseWriter, r *http.Request) {
		subPath := strings.TrimPrefix(r.URL.Path, "/api/mailpit/")
		mailpitURL := "http://mailpit:8025/api/v1/" + subPath

		resp, err := http.Get(mailpitURL)
		if err != nil {
			http.Error(w, "Failed to connect to internal Mailpit service: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index" {
			path = "index.html"
		}

		data, err := fs.ReadFile(publicFS, path)
		if err != nil && !strings.Contains(path, ".") {
			path = path + ".html"
			data, err = fs.ReadFile(publicFS, path)
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Inject server session ID into response if placeholder exists
		output := bytes.ReplaceAll(data, []byte("{{SERVER_SESSION_ID}}"), []byte(serverSessionID))

		if strings.HasSuffix(path, ".html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else if strings.HasSuffix(path, ".js") {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		} else if strings.HasSuffix(path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(output)
	})

	log.Printf("Web UI server listening on port %s (Session ID: %s)...", port, serverSessionID)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Web UI server failed: %v", err)
	}
}

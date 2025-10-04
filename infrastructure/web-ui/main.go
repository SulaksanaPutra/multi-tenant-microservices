package main

import (
	"bytes"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed index.html
var content embed.FS

var serverSessionID = fmt.Sprintf("session_%d", time.Now().UnixNano())

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := content.ReadFile("index.html")
		if err != nil {
			http.Error(w, "Could not read index.html", http.StatusInternalServerError)
			return
		}

		// Inject server session ID into HTML response
		output := bytes.ReplaceAll(data, []byte("{{SERVER_SESSION_ID}}"), []byte(serverSessionID))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(output)
	})

	log.Printf("Web UI server listening on port %s (Session ID: %s)...", port, serverSessionID)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Web UI server failed: %v", err)
	}
}

// Command hello-web is the pushrun demo service: a tiny HTTP server with a
// health endpoint. pushrun builds it on push and starts it with the leased
// port passed as -port (the pipeline forwards $CI_PORT).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.Int("port", 8080, "TCP port to listen on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello from pushrun")
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("hello-web listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

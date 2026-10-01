package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8091", "HTTP listen address")
	flag.Parse()
	log.Printf("TODO app: http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, NewHandler()))
}

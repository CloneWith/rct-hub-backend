package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8091", "local listen address")
	flag.Parse()

	lab, err := newLab("ready")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("RCTS1 MatchEngine Lab: http://%s\n", *address)
	if err := http.ListenAndServe(*address, lab.routes()); err != nil {
		log.Fatal(err)
	}
}

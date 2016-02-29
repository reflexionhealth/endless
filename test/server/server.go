package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/reflexionhealth/endless"
)

func handler(w http.ResponseWriter, r *http.Request) {
	time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
	w.Write([]byte("bar\n"))
}

func main() {
	log.SetFlags(0)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	pid := syscall.Getpid()
	log.Printf("[%d] Created\n", pid)
	endable := endless.NewServer("localhost:4242", mux)
	endable.ErrorLog = log.New(os.Stderr, fmt.Sprintf("[%d] ", pid), 0)
	endable.BeforeStart = func() { log.Printf("[%d] Starting\n", pid) }
	endable.BeforeRestart = func() { log.Printf("[%d] Restarting\n", pid) }
	endable.BeforeShutdown = func() { log.Printf("[%d] Stopping\n", pid) }
	endable.AfterShutdown = func(timeout bool) {
		if timeout {
			log.Printf("[%d] Timeout\n", pid)
		} else {
			log.Printf("[%d] Stopped\n", pid)
		}
	}
	err := endable.ListenAndServe()
	if err != nil {
		log.Printf("[%d] Error: %s\n", pid, err)
	}
	log.Printf("[%d] Exiting\n", pid)
	os.Exit(0)
}

package main

import (
	"fmt"
	"net/http"
	"sync"
)

func startUserService(wg *sync.WaitGroup) {
	defer wg.Done()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"service": "user", "path": "%s", "port": "8081"}`, r.URL.Path)
	})
	server := &http.Server{Addr: ":8081", Handler: mux}
	fmt.Println("User Service running on :8081")
	if err := server.ListenAndServe(); err != nil {
		fmt.Println("User Service failed:", err)
	}
}

func startUserService2(wg *sync.WaitGroup) {
	defer wg.Done()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"service": "user", "path": "%s", "port": "8083"}`, r.URL.Path)
	})
	server := &http.Server{Addr: ":8083", Handler: mux}
	fmt.Println("User Service 2 running on :8083")
	if err := server.ListenAndServe(); err != nil {
		fmt.Println("User Service 2 failed:", err)
	}
}

func startOrderService(wg *sync.WaitGroup) {
	defer wg.Done()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"service": "order", "path": "%s", "port": "8082"}`, r.URL.Path)
	})
	server := &http.Server{Addr: ":8082", Handler: mux}
	fmt.Println("Order Service running on :8082")
	if err := server.ListenAndServe(); err != nil {
		fmt.Println("Order Service failed:", err)
	}
}

func main() {
	var wg sync.WaitGroup
	wg.Add(3)

	go startUserService(&wg)
	go startUserService2(&wg)
	go startOrderService(&wg)

	wg.Wait()
}

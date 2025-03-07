package main

import (
	"fmt"
	"net/http"
	"sync"
)

func startUserService(wg *sync.WaitGroup) {
	defer wg.Done()
	http.HandleFunc("/user/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"service": "user", "path": "%s"}`, r.URL.Path)
	})
	fmt.Println("User Service running on :8081")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		fmt.Println("User Service failed:", err)
	}
}

func startOrderService(wg *sync.WaitGroup) {
	defer wg.Done()
	http.HandleFunc("/order/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"service": "order", "path": "%s"}`, r.URL.Path)
	})
	fmt.Println("Order Service running on :8082")
	if err := http.ListenAndServe(":8082", nil); err != nil {
		fmt.Println("Order Service failed:", err)
	}
}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)

	go startUserService(&wg)
	go startOrderService(&wg)

	wg.Wait()
}

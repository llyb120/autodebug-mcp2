package main

import (
	"fmt"
	"net/http"
	"time"
)

func main() {
	// 健康检查处理器
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	// 根路径处理器
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from test server at %s", time.Now().Format(time.RFC3339))
	})

	// 启动服务器
	fmt.Println("Test server starting on port 18080...")
	if err := http.ListenAndServe(":18080", nil); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

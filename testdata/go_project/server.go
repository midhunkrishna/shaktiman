package main

import (
	"fmt"
	"net/http"
)

// Server holds the HTTP server configuration.
type Server struct {
	Port    int
	Handler http.Handler
}

// NewServer creates a new Server with the given port.
func NewServer(port int) *Server {
	mux := http.NewServeMux()
	s := &Server{
		Port:    port,
		Handler: mux,
	}
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/users", s.handleUsers)
	return s
}

// Listen starts the HTTP server.
func (s *Server) Listen() error {
	addr := fmt.Sprintf(":%d", s.Port)
	fmt.Printf("Server listening on %s\n", addr)
	return http.ListenAndServe(addr, s.Handler)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"users": []}`)
}

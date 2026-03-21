package main

import (
	"fmt"
	"os"
)

func main() {
	store := NewStore()
	_ = store.AddUser(&User{
		ID:    "1",
		Name:  "Alice",
		Email: "alice@example.com",
	})

	server := NewServer(8080)
	if err := server.Listen(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

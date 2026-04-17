//go:build bruteforce

package testutil

import (
	// Registers the bruteforce vector backend with the vector registry so
	// tests built with the `bruteforce` tag can construct it by name.
	_ "github.com/shaktimanai/shaktiman/internal/vector/bruteforce"
)

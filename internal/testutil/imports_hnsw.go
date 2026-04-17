//go:build hnsw

package testutil

import (
	// Registers the hnsw vector backend with the vector registry so tests
	// built with the `hnsw` tag can construct it by name.
	_ "github.com/shaktimanai/shaktiman/internal/vector/hnsw"
)

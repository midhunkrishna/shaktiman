//go:build sqlite

package testutil

import (
	// Registers the sqlite metadata backend with the storage registry so
	// tests built with the `sqlite` tag can construct it by name.
	_ "github.com/shaktimanai/shaktiman/internal/storage/sqlite"
)

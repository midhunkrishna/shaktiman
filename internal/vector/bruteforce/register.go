package bruteforce

import (
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("brute_force", func(cfg vector.VectorStoreConfig) (types.VectorStore, error) {
		return NewBruteForceStore(cfg.Dims), nil
	})
}

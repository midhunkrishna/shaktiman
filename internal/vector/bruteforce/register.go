package bruteforce

import (
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func init() {
	vector.RegisterVectorStore("brute_force", func(cfg vector.StoreConfig) (types.VectorStore, error) {
		return NewStore(cfg.Dims), nil
	})
}

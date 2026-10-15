package agent

import (
	"context"
	"sync"

	"helperpay/records"
)

// LocalBlobs keeps uploads in memory for the dev server.
type LocalBlobs struct {
	mu    sync.Mutex
	items map[string]blob
}

type blob struct {
	data []byte
	ct   string
}

func NewLocalBlobs() *LocalBlobs { return &LocalBlobs{items: map[string]blob{}} }

func (b *LocalBlobs) Put(key string, data []byte, ct string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items[key] = blob{data, ct}
}

func (b *LocalBlobs) Get(_ context.Context, key string) ([]byte, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.items[key]
	if !ok {
		return nil, "", records.ErrNotFound
	}
	return it.data, it.ct, nil
}

func (b *LocalBlobs) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = map[string]blob{}
}

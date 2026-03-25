package pos

import (
	"encoding/json"
	"fmt"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Factory
}

func NewRegistry() *Registry {
	registry := &Registry{
		providers: map[string]Factory{},
	}

	registry.Register("generic", func(config json.RawMessage) (Provider, error) {
		return NewGenericProvider("generic"), nil
	})

	return registry
}

func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = factory
}

func (r *Registry) Build(name string, config json.RawMessage) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.providers[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("pos provider %q not registered", name)
	}
	return factory(config)
}

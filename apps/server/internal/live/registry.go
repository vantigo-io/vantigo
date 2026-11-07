package live

import (
	"fmt"
	"sync"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Registry is the topics the enabled modules serve and the user directory
// presence names its members through (L9). Compose fills it, before any
// request is served, from every enabled module's LiveTopics; the hub reads it.
type Registry struct {
	mu     sync.RWMutex
	topics map[string]Topic
	users  contracts.UserDirectory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{topics: map[string]Topic{}}
}

// Register adds module's topics. It refuses a kind outside the grammar, one
// whose module prefix is not module (the way permission keys are checked), a
// topic without Authorize, and a kind already registered.
func (r *Registry) Register(module string, topics ...Topic) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range topics {
		if !kindPattern.MatchString(t.Kind) {
			return fmt.Errorf("live: topic kind %q of module %q does not follow <module>.<kind>", t.Kind, module)
		}
		if moduleOf(t.Kind) != module {
			return fmt.Errorf("live: topic kind %q is not prefixed by its module %q", t.Kind, module)
		}
		if t.Authorize == nil {
			return fmt.Errorf("live: topic kind %q has no Authorize", t.Kind)
		}
		if _, dup := r.topics[t.Kind]; dup {
			return fmt.Errorf("live: duplicate topic kind %q", t.Kind)
		}
		r.topics[t.Kind] = t
	}
	return nil
}

// SetUsers sets the user directory presence resolves names through.
func (r *Registry) SetUsers(users contracts.UserDirectory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = users
}

// Kinds lists the registered kinds, for a test.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.topics))
	for k := range r.topics {
		out = append(out, k)
	}
	return out
}

func (r *Registry) lookup(kind string) (Topic, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.topics[kind]
	return t, ok
}

func (r *Registry) directory() contracts.UserDirectory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.users
}

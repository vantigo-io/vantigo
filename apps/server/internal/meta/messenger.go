package meta

import (
	"sync"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// providerKey and providerName are what a channel's provider column holds and
// what an admin reads.
const (
	providerKey  = "meta"
	providerName = "Facebook Messenger"
)

// messenger is the module's contracts.ChannelMessenger (Messenger design N2).
// Key and DisplayName are here, Status and Capabilities in settings.go, the
// four reads Communications' sync makes in inbound.go, Send in send.go,
// Check and Endpoints in pages.go and Profile in profile.go.
type messenger struct {
	core
	graph *graphClient

	filesOnce sync.Once
	files     storage.ObjectStore
	filesErr  error
}

var _ contracts.ChannelMessenger = (*messenger)(nil)

// newMessenger builds the provider over d.
func newMessenger(d module.Deps) contracts.ChannelMessenger {
	return &messenger{core: newCore(d), graph: newGraphClient(d)}
}

// objects is the object store the attachments are kept in, built once.
func (m *messenger) objects() (storage.ObjectStore, error) {
	m.filesOnce.Do(func() { m.files, m.filesErr = inboundStore(m.deps) })
	return m.files, m.filesErr
}

// Key is the provider's key, "meta".
func (m *messenger) Key() string { return providerKey }

// DisplayName is the provider's name as an admin reads it.
func (m *messenger) DisplayName() string { return providerName }

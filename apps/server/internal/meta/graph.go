package meta

import "github.com/vantigo-io/vantigo/server/internal/module"

// graphClient is the guarded client to Meta's Graph API.
type graphClient struct{}

// newGraphClient builds the client over d.
func newGraphClient(module.Deps) *graphClient { return &graphClient{} }

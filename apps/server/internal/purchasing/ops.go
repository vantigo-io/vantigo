package purchasing

import (
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// Operations is the module's contract operations as Go calls over d, for
// the demo seed, which writes its purchase orders through the module's own
// rules. A refusal comes back as the operation's typed response.
func Operations(d module.Deps) (gen.StrictServerInterface, error) { return newServer(d) }

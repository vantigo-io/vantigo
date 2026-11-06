package meta

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Profile is what Meta knows of one of the Page's participants.
func (m *messenger) Profile(context.Context, string, string) (contracts.MessengerProfile, error) {
	return contracts.MessengerProfile{}, errNotBuilt
}

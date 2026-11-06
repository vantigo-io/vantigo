package meta

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Send sends one part of a message from the Page to part.Participant.
func (m *messenger) Send(context.Context, string, contracts.MessengerPart) (contracts.MessengerSent, error) {
	return contracts.MessengerSent{}, errNotBuilt
}

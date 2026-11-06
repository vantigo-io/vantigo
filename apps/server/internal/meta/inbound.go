package meta

import (
	"context"
	"io"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Notified is the Pages with events not yet acknowledged that arrived after since.
func (m *messenger) Notified(context.Context, time.Time) ([]string, error) { return nil, errNotBuilt }

// Pending reads the Page's events after the cursor, oldest first.
func (m *messenger) Pending(context.Context, string, int64, int) ([]contracts.MessengerEvent, error) {
	return nil, errNotBuilt
}

// OpenAttachment opens one attachment of an event.
func (m *messenger) OpenAttachment(context.Context, string, string) (io.ReadCloser, contracts.MessengerFile, error) {
	return nil, contracts.MessengerFile{}, errNotBuilt
}

// Ack acknowledges the Page's events up to and including upTo.
func (m *messenger) Ack(context.Context, string, int64) error { return errNotBuilt }

package meta

import (
	"context"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The Messenger platform's limits (Messenger design N4) and window (N6).
const (
	maxText            = 2000
	maxAnswerOptions   = 13
	maxAnswerTitle     = 20
	maxAttachmentBytes = 25 << 20
	replyWindow        = 24 * time.Hour
	humanAgentWindow   = 7 * 24 * time.Hour
)

// capabilitiesTimeout bounds the one settings read Capabilities makes.
const capabilitiesTimeout = 2 * time.Second

// Status is whether channels may use the provider now: available once the app
// id and the app secret are stored, else not_configured. It reads the
// module's own row and never the network. A Page whose token Meta refused does
// not make the provider unavailable: that Page's endpoint is.
func (m *messenger) Status(ctx context.Context) (contracts.MessengerStatus, error) {
	s, err := m.settings(ctx)
	if err != nil {
		return contracts.MessengerStatus{}, err
	}
	if !configured(s) {
		return contracts.MessengerStatus{Reason: reasonNotConfigured}, nil
	}
	return contracts.MessengerStatus{Available: true}, nil
}

// Capabilities is what a message on Messenger may carry and how long a reply
// may follow the person's last message. No carousel until Communications can
// serve Meta an image's address (MaxCards stays zero); the Human Agent window
// is there only while the Owner has ticked the setting. A settings row that
// cannot be read is no Human Agent window: the shorter, safer one.
func (m *messenger) Capabilities() contracts.MessengerCapabilities {
	caps := contracts.MessengerCapabilities{
		MaxText:            maxText,
		MaxAnswerOptions:   maxAnswerOptions,
		MaxAnswerTitle:     maxAnswerTitle,
		ShareEmail:         true,
		MaxAttachmentBytes: maxAttachmentBytes,
		Window:             replyWindow,
	}
	ctx, cancel := context.WithTimeout(context.Background(), capabilitiesTimeout)
	defer cancel()
	if s, err := m.settings(ctx); err == nil && s.HumanAgentGranted {
		caps.HumanAgentWindow = humanAgentWindow
	} else if err != nil {
		m.logger.Warn("meta: the Human Agent setting could not be read; the 24 hour window applies", "err", err)
	}
	return caps
}

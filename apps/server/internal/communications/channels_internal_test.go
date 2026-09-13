package communications

import (
	"context"
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// TestVerifyChannel_DistinguishesProviderFromMissingCredentials is fix
// round 1's item 4's teeth check: before the fix, a channel with no
// credential row at all fell into the same branch as a genuinely
// unsupported provider and was told its provider "is only supported for
// smtp channels" — misdirecting for a channel whose provider already is
// smtp. Both cases still return a non-nil error (the wire response is the
// same 422 verification_failed either way — see the doc comment on
// errChannelMissingCredentials), so this is a white-box test, in the
// package itself rather than communications_test: the two failure modes
// are indistinguishable from the HTTP surface by design, and the only way
// to prove verifyChannel is reporting the right *reason* is to inspect the
// error value it actually returns.
func TestVerifyChannel_DistinguishesProviderFromMissingCredentials(t *testing.T) {
	s := &server{}

	wrongProvider := store.GetChannelByIDRow{Provider: "mailgun", CredentialSettingsJson: nil}
	if err := s.verifyChannel(context.Background(), nil, wrongProvider); !errors.Is(err, errUnsupportedChannelProvider) {
		t.Errorf("verifyChannel(mailgun, no credentials) = %v, want errUnsupportedChannelProvider", err)
	}

	noCredentials := store.GetChannelByIDRow{Provider: "smtp", CredentialSettingsJson: nil}
	if err := s.verifyChannel(context.Background(), nil, noCredentials); !errors.Is(err, errChannelMissingCredentials) {
		t.Errorf("verifyChannel(smtp, no credentials) = %v, want errChannelMissingCredentials, not errUnsupportedChannelProvider", err)
	}
}

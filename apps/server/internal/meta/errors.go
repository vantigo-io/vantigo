package meta

import (
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"

	"context"
	"fmt"

	"github.com/google/uuid"
)

// This module's refusals (Messenger design N10) are RFC 9457 problems in one
// shape, gen.MetaProblem: field errors on the body (400) keyed by the
// request's camelCase field, or a status carrying one of the codes below. The
// 401, the 403 and the 429 are the router's own. A client keys off the status
// and the code, never the words.

// The refusal codes.
const (
	// codePublicHostRequired is a settings write on an instance with no public
	// host (422): the webhook lives there.
	codePublicHostRequired = "public_host_required"
	// codeNotConfigured is a Page added before the app id and secret are set
	// (422).
	codeNotConfigured = "not_configured"
	// codePageTokenInsufficient is a token that is not a Page's, or may not
	// message it (422).
	codePageTokenInsufficient = "page_token_insufficient"
	// codeMetaUnreachable is a call to Meta that could not be made (503).
	codeMetaUnreachable = "meta_unreachable"
	// codePageNotFound is a Page that is not connected (404).
	codePageNotFound = "page_not_found"
)

// The unavailability reasons the ChannelMessenger answers (codes, never
// Meta's words).
const (
	// reasonNotConfigured: the app id or secret is not set.
	reasonNotConfigured = "not_configured"
	// reasonTokenRejected: Meta refused the Page's token (code 190).
	reasonTokenRejected = "token_rejected"
)

// fieldErrors is a validation pass's outcome: every failure, by field.
type fieldErrors struct {
	errs map[string][]string
}

// add records message on field; "" records nothing.
func (f *fieldErrors) add(field, message string) {
	if message == "" {
		return
	}
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.MetaProblem {
	status := int32(400)
	errs := f.errs
	return gen.MetaProblem{Title: &title, Status: &status, Errors: &errs}
}

// problem is a refusal of status carrying the rule's code and a sentence for
// a person.
func problem(status int, code, title, detail string) gen.MetaProblem {
	s := int32(status)
	return gen.MetaProblem{Code: &code, Title: &title, Detail: &detail, Status: &s}
}

// requestActor is the signed-in user a write is made by.
func requestActor(ctx context.Context) (uuid.UUID, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("meta: a write without a signed-in user: %w", contracts.ErrInvalidActor)
	}
	return p.UserID, nil
}

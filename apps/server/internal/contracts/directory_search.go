package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrDirectoryUnavailable is what a DirectorySearch wraps a failure to ask
// its directory in: the provider's credential refused, the directory not
// answering, throttling, or a read that ran past the provider's bound.
// Identity answers it 503 provider_unreachable; any other error is a fault.
var ErrDirectoryUnavailable = errors.New("contracts: the directory could not be asked")

// What matched a candidate to a directory account (DirectoryMatch.MatchedBy).
const (
	// DirectoryMatchedByMail: the user's address equals the account's mail,
	// ignoring case.
	DirectoryMatchedByMail = "mail"
	// DirectoryMatchedByPrincipalName: the user's address equals the
	// account's principal name, ignoring case.
	DirectoryMatchedByPrincipalName = "principalName"
	// DirectoryMatchedByEmployeeNumber: the user's employee number equals the
	// account's, trimmed and ignoring case.
	DirectoryMatchedByEmployeeNumber = "employeeNumber"
)

// DirectorySearch is a directory's accounts as identity offers them to an
// Owner connecting existing users (module-boundaries rule 22; Google
// Workspace design G8). Many providers, keyed by Key; identity asks the one
// whose Key is the configured OIDC provider. Every call reaches the network
// and is never made inside a transaction.
//
// A provider reads what it needs of identity — the users not yet connected
// and who holds a key — through SSOConnections, with the request's context
// identity hands it, so identity's own gatekeeping applies unchanged.
// A user's address is compared inside the provider and goes nowhere else.
type DirectorySearch interface {
	// Key is the OIDC provider it serves: "entra", "google".
	Key() string
	// DisplayName is the directory as a person reads it: "Microsoft Entra
	// ID", "Google Workspace".
	DisplayName() string
	// Users answers up to limit accounts whose name or address starts with
	// search, each with its holder under the configured issuer. A failure to
	// ask the directory wraps ErrDirectoryUnavailable.
	Users(ctx context.Context, search string, limit int) ([]DirectoryUser, error)
	// Matches answers the exact matches between SSOConnections.Unconnected
	// and the directory: a candidate is proposed only when every key that
	// matched points to one account no other candidate's key matched, and
	// that account is held by nobody else. ErrSSONotConfigured while OIDC is
	// off; a failure to ask the directory wraps ErrDirectoryUnavailable.
	Matches(ctx context.Context) (DirectoryMatches, error)
}

// DirectoryUser is one account in a directory. ObjectID and PrincipalName
// are the two keys an announced link names (identity's objectId and
// userPrincipalName); Mail and EmployeeNumber are "" when the account has
// none.
type DirectoryUser struct {
	ObjectID, PrincipalName, DisplayName, Mail, EmployeeNumber string
	// Enabled is whether the account may sign in; Guest whether it is a
	// guest invited from another organisation.
	Enabled, Guest bool
	// Holder is who already holds the account's keys under the configured
	// issuer, nil when nobody does (always nil in a DirectoryMatch).
	Holder *KeyHolder
}

// DirectoryMatches is Matches' answer.
type DirectoryMatches struct {
	// Matches are the proposals, in the order identity lists the users.
	Matches []DirectoryMatch
	// Candidates is how many users not yet connected were read; Ambiguous
	// how many of them were left out because more than one account was in
	// play.
	Candidates, Ambiguous int
	// Truncated is true when more users were waiting than the provider
	// reads.
	Truncated bool
}

// DirectoryMatch is a user not yet connected and the one account that
// matches them exactly.
type DirectoryMatch struct {
	UserID  uuid.UUID
	Account DirectoryUser
	// MatchedBy is every key that matched, DirectoryMatchedByMail,
	// DirectoryMatchedByPrincipalName, DirectoryMatchedByEmployeeNumber, in
	// that order.
	MatchedBy []string
}

package staff

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file and objects.go are the whole of this module's reach outside its
// own schema: the user directory (deps.Users, identity's
// contracts.UserDirectory, always composed) and the SMTP seam (deps.SMTPSend,
// or the platform's guarded mail.SendOutbound) here, and the object store the
// documents' bytes live in there (delivery A2). Every call is made through
// one of the thin accessors and through nowhere else, so "what does Staff ask
// of its neighbours, and when" has one place to read the answer and one place
// to check it from.
//
// What is checked is the lock rule (design §4, Locking; module-boundaries rule
// 10 restated): no call that takes its own connection or leaves the process
// is made inside one of this module's transactions — a locked one, or a
// read-only snapshot, which holds a pool connection just the same. Every
// accessor reports its call to contractCallHook, which the tests install to
// record it with whether it was made inside one, and every accessor refuses a
// call made inside one with errCallInsideTransaction: the mistake is a
// programming error, and failing the request loudly is better than holding a
// connection or a lock across a directory read or a mail server. The cost in
// production is one context lookup per call.

// errCallInsideTransaction is every accessor's answer to a call made inside
// one of this module's transactions. Nothing answers it to a client except as
// the 500 any other fault is.
var errCallInsideTransaction = errors.New("staff: a call out of the module was made inside one of its transactions")

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs,
// by this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call out of the module, and answers whether it
// may go ahead: never from inside one of the module's transactions.
func noteContractCall(ctx context.Context, method string) error {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
	if inOpenTx(ctx) {
		return errCallInsideTransaction
	}
	return nil
}

// usersUser, usersUsers and usersSearch are identity's user directory, the
// only way this module may read a user: the names on a person's link, the
// user a link names, the picker HR links a person with.
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	return s.deps.Users.User(ctx, id)
}

// usersUsers answers nothing for an empty batch without asking anybody: a
// cross-module call that can only answer nothing is a call worth not making.
func (s *server) usersUsers(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Users.Users"); err != nil {
		return nil, err
	}
	return s.deps.Users.Users(ctx, ids)
}

func (s *server) usersSearch(ctx context.Context, query string, limit int) ([]contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.SearchUsers"); err != nil {
		return nil, err
	}
	return s.deps.Users.SearchUsers(ctx, query, limit)
}

// smtpSend hands one mail to the installation's SMTP server: Deps.SMTPSend
// when a harness set one, otherwise mail.SendOutbound, the platform's guarded
// path (module-boundaries rule 1). It is Invoices' accessor
// (internal/invoices/contractscalls.go), the way a module sends with the
// installation's own SMTP_* configuration. A caller asks mailAvailable first.
func (s *server) smtpSend(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error {
	if err := noteContractCall(ctx, "SMTPSend"); err != nil {
		return err
	}
	if s.deps.SMTPSend != nil {
		return s.deps.SMTPSend(ctx, cfg, out)
	}
	return mail.SendOutbound(ctx, cfg, out)
}

// mailAvailable is whether this installation can send a mail at all: its mail
// driver is smtp. The log driver delivers nothing, so a mail there is skipped
// with a log line rather than attempted (design S12, delivery A1's bank-change
// mail).
func (s *server) mailAvailable() bool {
	return s.deps.Config != nil && s.deps.Config.Mail.Driver == "smtp"
}

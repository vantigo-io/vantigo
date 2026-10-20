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
// contracts.UserDirectory, always composed), account provisioning
// (deps.Accounts, identity's contracts.AccountProvisioning — delivery C's
// write direction, staff → identity), the identity provisioners
// (deps.IdentityProvisioners, a provider module's
// contracts.IdentityProvisioner — Entra delivery B's, staff → a provider's
// directory, module-boundaries rule 13) and the SMTP seam (deps.SMTPSend,
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

// usersPermissionHolders is the active users holding key, with the address
// to mail each at: read only by the holders' rules (holders.go), and the
// address only by the absence mails — never put on the wire or in a log.
func (s *server) usersPermissionHolders(ctx context.Context, key string) ([]contracts.Holder, error) {
	if err := noteContractCall(ctx, "Users.PermissionHolders"); err != nil {
		return nil, err
	}
	return s.deps.Users.PermissionHolders(ctx, key)
}

// errAccountsUnavailable is every accounts accessor's answer when no module
// provides account provisioning (Deps.Accounts nil): the request is answered
// accounts_unavailable, or the field it fills is left out.
var errAccountsUnavailable = errors.New("staff: no module provides account provisioning")

// accountsMode, accountsAccount and accountsProvisioningRoles are identity's
// account provisioning read side (contracts.AccountProvisioning): the
// authority mode, a user as identity reports them, and the roles an Owner
// named for Staff. The writes follow them.
func (s *server) accountsMode(ctx context.Context) (string, error) {
	if err := noteContractCall(ctx, "Accounts.Mode"); err != nil {
		return "", err
	}
	if s.deps.Accounts == nil {
		return "", errAccountsUnavailable
	}
	return s.deps.Accounts.Mode(ctx)
}

// accountsAccount is the user id names as identity reports them, nil when
// identity knows no such user. The address identity compares its own with
// (AccountRef.EmailDiffers) is person's own stored one, by their choice
// (accountAddress), and none when person is nil: the accessor takes the
// person, never an address a request carries. HR writes the person's
// address, so staff:manage can still make one guess per run at identity's
// address — each guess a person_updated event; accepted (privacy review L1).
func (s *server) accountsAccount(ctx context.Context, id uuid.UUID, person *accountPerson) (*contracts.AccountRef, error) {
	if err := noteContractCall(ctx, "Accounts.Account"); err != nil {
		return nil, err
	}
	if s.deps.Accounts == nil {
		return nil, errAccountsUnavailable
	}
	want := ""
	if person != nil {
		want = accountAddress(*person)
	}
	return s.deps.Accounts.Account(ctx, id, want)
}

func (s *server) accountsProvisioningRoles(ctx context.Context) ([]contracts.RoleRef, error) {
	if err := noteContractCall(ctx, "Accounts.ProvisioningRoles"); err != nil {
		return nil, err
	}
	if s.deps.Accounts == nil {
		return nil, errAccountsUnavailable
	}
	return s.deps.Accounts.ProvisioningRoles(ctx)
}

// accountsPrepare, accountsActivate, accountsDeactivate and accountsUpdate
// are identity's account provisioning write side: the accounts worker's
// steps (accounts_worker.go), and Run and Disable now's (Task 4c). Each is
// called on no connection of this module's — the claim committed, the
// snapshot closed — so the call holds no lock and no connection while
// identity works.
func (s *server) accountsPrepare(ctx context.Context, actor contracts.Actor, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	if err := noteContractCall(ctx, "Accounts.Prepare"); err != nil {
		return contracts.AccountRef{}, err
	}
	if s.deps.Accounts == nil {
		return contracts.AccountRef{}, errAccountsUnavailable
	}
	return s.deps.Accounts.Prepare(ctx, actor, spec)
}

func (s *server) accountsActivate(ctx context.Context, actor contracts.Actor, id uuid.UUID, opts contracts.ActivateOpts) (contracts.AccountRef, error) {
	if err := noteContractCall(ctx, "Accounts.Activate"); err != nil {
		return contracts.AccountRef{}, err
	}
	if s.deps.Accounts == nil {
		return contracts.AccountRef{}, errAccountsUnavailable
	}
	return s.deps.Accounts.Activate(ctx, actor, id, opts)
}

func (s *server) accountsDeactivate(ctx context.Context, actor contracts.Actor, id uuid.UUID) (contracts.AccountRef, error) {
	if err := noteContractCall(ctx, "Accounts.Deactivate"); err != nil {
		return contracts.AccountRef{}, err
	}
	if s.deps.Accounts == nil {
		return contracts.AccountRef{}, errAccountsUnavailable
	}
	return s.deps.Accounts.Deactivate(ctx, actor, id)
}

func (s *server) accountsUpdate(ctx context.Context, actor contracts.Actor, id uuid.UUID, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	if err := noteContractCall(ctx, "Accounts.Update"); err != nil {
		return contracts.AccountRef{}, err
	}
	if s.deps.Accounts == nil {
		return contracts.AccountRef{}, errAccountsUnavailable
	}
	return s.deps.Accounts.Update(ctx, actor, id, spec)
}

// accountsExpect and accountsUnexpect are identity's announcement of an
// external account (Entra design D4): the account a provider prepared for a
// person, so identity adopts the user the provider's SCIM push or the
// person's first sign-in creates, and its withdrawal when nothing came.
func (s *server) accountsExpect(ctx context.Context, actor contracts.Actor, spec contracts.ExpectedAccount) error {
	if err := noteContractCall(ctx, "Accounts.Expect"); err != nil {
		return err
	}
	if s.deps.Accounts == nil {
		return errAccountsUnavailable
	}
	return s.deps.Accounts.Expect(ctx, actor, spec)
}

func (s *server) accountsUnexpect(ctx context.Context, actor contracts.Actor, employeeNumber string) error {
	if err := noteContractCall(ctx, "Accounts.Unexpect"); err != nil {
		return err
	}
	if s.deps.Accounts == nil {
		return errAccountsUnavailable
	}
	return s.deps.Accounts.Unexpect(ctx, actor, employeeNumber)
}

// errProviderUnavailable is every provider accessor's answer when no enabled
// module offers the provider a person's kind names (the slot lacks its key):
// the worker records provider_unavailable, a request answers 503.
var errProviderUnavailable = errors.New("staff: no module offers the identity provider")

// provisioner is the provider in the slot (Deps.IdentityProvisioners) under
// key — Staff writes a provider's directory (module-boundaries rule 13) —
// false when no enabled module offers it. It reads the slot alone and calls
// nothing.
func (s *server) provisioner(key string) (contracts.IdentityProvisioner, bool) {
	for _, p := range s.deps.IdentityProvisioners {
		if p != nil && p.Key() == key {
			return p, true
		}
	}
	return nil, false
}

// providerInfo, providerAccount, providerPrepare, providerActivate,
// providerDeactivate, providerUpdate, providerDelete and providerRevealPass
// are a provider's accounts (contracts.IdentityProvisioner): each reaches
// the network, so each is called on no connection of this module's, under
// the worker's call timeout, and refuses a call inside a transaction. p nil
// (provisioner found none) answers errProviderUnavailable.
func (s *server) providerInfo(ctx context.Context, p contracts.IdentityProvisioner) (contracts.ProviderInfo, error) {
	if err := noteContractCall(ctx, "Provisioner.Provider"); err != nil {
		return contracts.ProviderInfo{}, err
	}
	if p == nil {
		return contracts.ProviderInfo{}, errProviderUnavailable
	}
	return p.Provider(ctx)
}

func (s *server) providerAccount(ctx context.Context, p contracts.IdentityProvisioner, externalID string) (*contracts.ExternalAccount, error) {
	if err := noteContractCall(ctx, "Provisioner.Account"); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProviderUnavailable
	}
	return p.Account(ctx, externalID)
}

func (s *server) providerPrepare(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if err := noteContractCall(ctx, "Provisioner.Prepare"); err != nil {
		return contracts.ExternalAccount{}, err
	}
	if p == nil {
		return contracts.ExternalAccount{}, errProviderUnavailable
	}
	return p.Prepare(ctx, actor, spec)
}

func (s *server) providerActivate(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if err := noteContractCall(ctx, "Provisioner.Activate"); err != nil {
		return contracts.ExternalAccount{}, err
	}
	if p == nil {
		return contracts.ExternalAccount{}, errProviderUnavailable
	}
	return p.Activate(ctx, actor, externalID, spec)
}

func (s *server) providerDeactivate(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if err := noteContractCall(ctx, "Provisioner.Deactivate"); err != nil {
		return contracts.ExternalAccount{}, err
	}
	if p == nil {
		return contracts.ExternalAccount{}, errProviderUnavailable
	}
	return p.Deactivate(ctx, actor, externalID, spec)
}

func (s *server) providerUpdate(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if err := noteContractCall(ctx, "Provisioner.Update"); err != nil {
		return contracts.ExternalAccount{}, err
	}
	if p == nil {
		return contracts.ExternalAccount{}, errProviderUnavailable
	}
	return p.Update(ctx, actor, externalID, spec)
}

func (s *server) providerDelete(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, externalID string) error {
	if err := noteContractCall(ctx, "Provisioner.Delete"); err != nil {
		return err
	}
	if p == nil {
		return errProviderUnavailable
	}
	return p.Delete(ctx, actor, externalID)
}

// providerRevealPass is the pass the provider issued for the account, once
// (contracts.ErrPassGone after): the one place a pass passes through this
// module, answered to the caller and never logged, stored or put on an event.
func (s *server) providerRevealPass(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, externalID string) (contracts.Pass, error) {
	if err := noteContractCall(ctx, "Provisioner.RevealPass"); err != nil {
		return contracts.Pass{}, err
	}
	if p == nil {
		return contracts.Pass{}, errProviderUnavailable
	}
	return p.RevealPass(ctx, actor, externalID)
}

// providerGroupTraits, providerEnsureGroup, providerSetMembers and
// providerDeleteGroup are a provider's groups (contracts.IdentityProvisioner,
// Entra delivery D): the traits it declares, the external group made or
// brought to the spec, its members reconciled, and the group deleted. Each
// reaches the network like the accounts' calls, so each refuses a call inside
// a transaction; p nil answers errProviderUnavailable.
func (s *server) providerGroupTraits(ctx context.Context, p contracts.IdentityProvisioner) ([]contracts.GroupTrait, error) {
	if err := noteContractCall(ctx, "Provisioner.GroupTraits"); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, errProviderUnavailable
	}
	return p.GroupTraits(ctx)
}

func (s *server) providerEnsureGroup(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	if err := noteContractCall(ctx, "Provisioner.EnsureGroup"); err != nil {
		return contracts.ExternalGroup{}, err
	}
	if p == nil {
		return contracts.ExternalGroup{}, errProviderUnavailable
	}
	return p.EnsureGroup(ctx, actor, spec)
}

func (s *server) providerSetMembers(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, groupID string, memberExternalIDs []string) (contracts.MembersResult, error) {
	if err := noteContractCall(ctx, "Provisioner.SetMembers"); err != nil {
		return contracts.MembersResult{}, err
	}
	if p == nil {
		return contracts.MembersResult{}, errProviderUnavailable
	}
	return p.SetMembers(ctx, actor, groupID, memberExternalIDs)
}

func (s *server) providerDeleteGroup(ctx context.Context, p contracts.IdentityProvisioner, actor contracts.Actor, groupID string) error {
	if err := noteContractCall(ctx, "Provisioner.DeleteGroup"); err != nil {
		return err
	}
	if p == nil {
		return errProviderUnavailable
	}
	return p.DeleteGroup(ctx, actor, groupID)
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

package modtest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeIdentityProvisioner is a contracts.IdentityProvisioner a test shapes:
// an external directory in memory, for Staff's provider link, the accounts
// worker's third column and the attention items to be tested before any
// provider module exists (Entra delivery B). It holds accounts by employee
// number (contracts.NormalizeEmployeeNumber's key) — each the
// contracts.ExternalAccount it answers, the spec last written to it, its
// pass and the facts it reports — and records every call but the reads
// (Provider, Account, GroupTraits) in Calls.
//
// Its writes act as a directory would: Prepare finds the account holding the
// number (ErrExternalAmbiguous when a test gave two accounts one number) or
// creates it disabled; Activate enables it and issues a pass; Deactivate
// disables it and clears the pass; Update records the spec; Delete refuses
// an enabled account and marks it deleted; RevealPass answers a pass once.
// An account SetUnconfirmed marks is an adoption nobody confirmed: every
// write to it answers ErrExternalUnconfirmed and changes nothing.
// Its groups validate as a provider does: GroupTraits declares a required
// "type" choice and a "mailNickname" string of at most 64 characters, and
// EnsureGroup answers a *contracts.GroupTraitError for a value they do not
// admit or a type that differs from the one the group was made with;
// Owners is what a group's last EnsureGroup named, and FailMembers makes
// the next SetMembers report a member as failed. A group SetGroupUnconfirmed
// marks is one the provider found and adopted and nobody has confirmed:
// EnsureGroup and SetMembers answer ErrGroupUnconfirmed for it and change
// nothing, and DeleteGroup forgets it, as a provider leaves a group it did
// not make to its directory.
// A write names a valid actor (contracts.ErrInvalidActor otherwise), and
// while the status is not connected every write but Deactivate answers
// ErrProviderUnavailable — Deactivate runs whatever the status says, as the
// contract has it. FailWith makes a method answer an error a test chooses.
// It is safe for concurrent use: the accounts worker calls it from its own
// goroutine while a test reads it.
type FakeIdentityProvisioner struct {
	mu       sync.Mutex
	key      string
	name     string
	status   string
	reason   string
	now      func() time.Time
	accounts map[string][]*fakeExternalAccount // by NormalizeEmployeeNumber key
	calls    []ProvisionerCall
	fail     map[string]error
	groups   map[string]*fakeExternalGroup // by the caller's group id
	// memberFailures are the members the next SetMembers of a group reports
	// as failed, by the caller's group id, then by external id: the code.
	memberFailures map[string]map[string]string
	// unconfirmedGroups are the caller's group ids SetGroupUnconfirmed
	// marked: adopted, not yet confirmed.
	unconfirmedGroups map[string]bool
}

// ProvisionerCall is one call a FakeIdentityProvisioner recorded: the method,
// the actor (its System, or its user's id), the external id it named — the
// caller's group id for SetMembers and DeleteGroup, "" for Prepare and
// EnsureGroup — and a copy of the spec it was given, nil for a call that
// takes none.
type ProvisionerCall struct {
	Method, Actor, ExternalID string
	Spec                      *contracts.ExternalSpec
}

// fakeExternalAccount is one account of the fake's directory.
type fakeExternalAccount struct {
	account     contracts.ExternalAccount
	spec        *contracts.ExternalSpec // the spec last written, nil before any
	pass        *fakePass
	unconfirmed bool // SetUnconfirmed: its writes answer ErrExternalUnconfirmed
}

// fakePass is an issued pass: its value, when it expires, and whether it was
// revealed.
type fakePass struct {
	value     string
	expiresAt time.Time
	revealed  bool
}

// fakeExternalGroup is one group of the fake's directory.
type fakeExternalGroup struct {
	externalID string
	spec       contracts.GroupSpec
	kind       string // the "type" the group was made with, which never changes
	owners     []string
	members    []string
}

// fakeNicknameMax is the longest mailNickname the fake's declaration admits.
const fakeNicknameMax = 64

var _ contracts.IdentityProvisioner = (*FakeIdentityProvisioner)(nil)

// fakePassLifetime is how long a pass the fake issues lives.
const fakePassLifetime = 8 * time.Hour

// errFakeNoSuchAccount answers a write naming an external id the fake's
// directory does not hold: a failure to retry, as a directory's 404 would be.
var errFakeNoSuchAccount = errors.New("modtest: the fake directory holds no such account")

// NewFakeIdentityProvisioner is a connected provider under key with the
// display name name, its directory empty, on the wall clock (WithClock).
func NewFakeIdentityProvisioner(key, name string) *FakeIdentityProvisioner {
	return &FakeIdentityProvisioner{
		key:      key,
		name:     name,
		status:   contracts.ProviderConnected,
		now:      time.Now,
		accounts: map[string][]*fakeExternalAccount{},
		fail:     map[string]error{},
		groups:   map[string]*fakeExternalGroup{},

		memberFailures:    map[string]map[string]string{},
		unconfirmedGroups: map[string]bool{},
	}
}

// WithClock makes the fake judge a pass's lifetime by now — a harness's
// Now, so a test that advances the harness clock expires the pass. It
// answers the fake, for a constructor's chain.
func (f *FakeIdentityProvisioner) WithClock(now func() time.Time) *FakeIdentityProvisioner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
	return f
}

// SetStatus sets what Provider answers: contracts.ProviderUnconfigured,
// ProviderConnected or ProviderFailing, with a reason code.
func (f *FakeIdentityProvisioner) SetStatus(status, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.reason = status, reason
}

// FailWith makes every call of method ("Provider", "Prepare", "Activate",
// "Deactivate", "Update", "Delete", "Account", "RevealPass", "GroupTraits",
// "EnsureGroup", "SetMembers", "DeleteGroup") answer err, nil to stop. A
// write is recorded before it answers err.
func (f *FakeIdentityProvisioner) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
		return
	}
	f.fail[method] = err
}

// ReportWith makes every account holding number report codes
// (ExternalAccount.Reported) in every answer from now on, none to stop.
func (f *FakeIdentityProvisioner) ReportWith(number string, codes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts[contracts.NormalizeEmployeeNumber(number)] {
		a.account.Reported = slices.Clone(codes)
	}
}

// SetUnconfirmed marks every account holding number as an adoption nobody
// has confirmed — Prepare, Activate, Deactivate, Update and Delete answer
// ErrExternalUnconfirmed for it, recorded, and change nothing — or, with
// unconfirmed false, as confirmed: its writes act again.
func (f *FakeIdentityProvisioner) SetUnconfirmed(number string, unconfirmed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts[contracts.NormalizeEmployeeNumber(number)] {
		a.unconfirmed = unconfirmed
	}
}

// SetAccount puts account in the directory as one holding number, as an
// administrator or another tool would have made it: an account the fake
// holds with the same ExternalID is replaced, any other is added beside the
// ones the number has — two make Prepare answer ErrExternalAmbiguous. An
// empty ExternalID is given one. It answers the account as stored.
func (f *FakeIdentityProvisioner) SetAccount(number string, account contracts.ExternalAccount) contracts.ExternalAccount {
	f.mu.Lock()
	defer f.mu.Unlock()
	if account.ExternalID == "" {
		account.ExternalID = uuid.NewString()
	}
	account.Created = false
	account.Reported = slices.Clone(account.Reported)
	if held := f.byID(account.ExternalID); held != nil {
		held.account = account
		return account
	}
	key := contracts.NormalizeEmployeeNumber(number)
	f.accounts[key] = append(f.accounts[key], &fakeExternalAccount{account: account})
	return account
}

// Gone removes every account holding number from the directory, as an
// administrator's hard delete would: Account answers nil for it from now on.
func (f *FakeIdentityProvisioner) Gone(number string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.accounts, contracts.NormalizeEmployeeNumber(number))
}

// ByNumber is the account holding number as the fake would answer it — the
// first, when a test gave the number two — and whether there is one.
func (f *FakeIdentityProvisioner) ByNumber(number string) (contracts.ExternalAccount, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	held := f.accounts[contracts.NormalizeEmployeeNumber(number)]
	if len(held) == 0 {
		return contracts.ExternalAccount{}, false
	}
	return f.answer(held[0]), true
}

// LastSpec is the spec last written to the account holding number, and
// whether one was.
func (f *FakeIdentityProvisioner) LastSpec(number string) (contracts.ExternalSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	held := f.accounts[contracts.NormalizeEmployeeNumber(number)]
	if len(held) == 0 || held[0].spec == nil {
		return contracts.ExternalSpec{}, false
	}
	return copySpec(*held[0].spec), true
}

// Owners is the owners' external ids the last EnsureGroup for the caller's
// groupID named, nil when the fake holds no such group.
func (f *FakeIdentityProvisioner) Owners(groupID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	group, ok := f.groups[groupID]
	if !ok {
		return nil
	}
	return slices.Clone(group.owners)
}

// FailMembers makes the next SetMembers of the caller's groupID report
// externalID in MembersResult.Failed with code (contracts.MemberNotFound,
// MemberDynamic, MemberRefused, MemberOther) and leave its membership as it
// was. Once: the SetMembers after that changes it as asked.
func (f *FakeIdentityProvisioner) FailMembers(groupID, externalID, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.memberFailures[groupID] == nil {
		f.memberFailures[groupID] = map[string]string{}
	}
	f.memberFailures[groupID][externalID] = code
}

// SetGroupUnconfirmed marks the caller's groupID as a group the provider
// found and adopted and nobody has confirmed — EnsureGroup and SetMembers
// answer contracts.ErrGroupUnconfirmed for it, recorded, and change nothing
// — or, with unconfirmed false, as confirmed: its writes act again.
func (f *FakeIdentityProvisioner) SetGroupUnconfirmed(groupID string, unconfirmed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if unconfirmed {
		f.unconfirmedGroups[groupID] = true
	} else {
		delete(f.unconfirmedGroups, groupID)
	}
}

// Calls is every write so far, in order, refused ones included.
func (f *FakeIdentityProvisioner) Calls() []ProvisionerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ProvisionerCall, len(f.calls))
	for i, c := range f.calls {
		out[i] = c
		if c.Spec != nil {
			spec := copySpec(*c.Spec)
			out[i].Spec = &spec
		}
	}
	return out
}

// Key is the provider's key, as NewFakeIdentityProvisioner was given it.
func (f *FakeIdentityProvisioner) Key() string { return f.key }

// Provider answers the key, the name and the status a test set.
func (f *FakeIdentityProvisioner) Provider(context.Context) (contracts.ProviderInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["Provider"]; err != nil {
		return contracts.ProviderInfo{}, err
	}
	return contracts.ProviderInfo{Key: f.key, Name: f.name, Status: f.status, Reason: f.reason}, nil
}

// Prepare finds the account holding spec.EmployeeNumber and writes the spec
// to it, or creates it disabled: the next external id, a user principal name
// of the given and family names folded to ASCII lower case at fake.example,
// the spec's work email as its address, else the principal name.
func (f *FakeIdentityProvisioner) Prepare(_ context.Context, actor contracts.Actor, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Prepare", actor, "", &spec, true); err != nil {
		return contracts.ExternalAccount{}, err
	}
	key := contracts.NormalizeEmployeeNumber(spec.EmployeeNumber)
	held := f.accounts[key]
	switch {
	case len(held) > 1:
		return contracts.ExternalAccount{}, contracts.ErrExternalAmbiguous
	case len(held) == 1 && held[0].unconfirmed:
		return contracts.ExternalAccount{}, contracts.ErrExternalUnconfirmed
	case len(held) == 1:
		if f.addressTaken(spec.WorkEmail, held[0]) {
			return contracts.ExternalAccount{}, contracts.ErrExternalAddressTaken
		}
		held[0].write(spec)
		return f.answer(held[0]), nil
	}
	if f.addressTaken(spec.WorkEmail, nil) {
		return contracts.ExternalAccount{}, contracts.ErrExternalAddressTaken
	}
	upn := f.freeUPN(spec.GivenName, spec.FamilyName)
	email := spec.WorkEmail
	if email == "" {
		email = upn
	}
	created := &fakeExternalAccount{account: contracts.ExternalAccount{
		ExternalID: uuid.NewString(), UserPrincipalName: upn, Email: email, State: "disabled",
	}}
	created.write(spec)
	f.accounts[key] = append(f.accounts[key], created)
	out := f.answer(created)
	out.Created = true
	return out, nil
}

// Activate enables the account and records the spec. It issues a pass, alive
// eight hours on the fake's clock, when the account was not enabled and
// holds no pass yet to reveal, or on HR's Run now (spec.Flags.Fresh) — which
// replaces one.
func (f *FakeIdentityProvisioner) Activate(_ context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Activate", actor, externalID, &spec, true); err != nil {
		return contracts.ExternalAccount{}, err
	}
	held := f.byID(externalID)
	switch {
	case held == nil:
		return contracts.ExternalAccount{}, errFakeNoSuchAccount
	case held.unconfirmed:
		return contracts.ExternalAccount{}, contracts.ErrExternalUnconfirmed
	}
	wasEnabled := held.account.State == "enabled"
	held.account.State = "enabled"
	held.write(spec)
	if spec.Flags.Fresh || (!wasEnabled && !f.livePass(held)) {
		held.pass = &fakePass{value: fakePassValue(), expiresAt: f.now().Add(fakePassLifetime)}
	}
	return f.answer(held), nil
}

// Deactivate disables the account, clears its pass and records the spec,
// whatever the status.
func (f *FakeIdentityProvisioner) Deactivate(_ context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Deactivate", actor, externalID, &spec, false); err != nil {
		return contracts.ExternalAccount{}, err
	}
	held := f.byID(externalID)
	switch {
	case held == nil:
		return contracts.ExternalAccount{}, errFakeNoSuchAccount
	case held.unconfirmed:
		return contracts.ExternalAccount{}, contracts.ErrExternalUnconfirmed
	}
	if held.account.State != "deleted" {
		held.account.State = "disabled"
	}
	held.pass = nil
	held.write(spec)
	return f.answer(held), nil
}

// Update records the spec; the account is otherwise as it was.
func (f *FakeIdentityProvisioner) Update(_ context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Update", actor, externalID, &spec, true); err != nil {
		return contracts.ExternalAccount{}, err
	}
	held := f.byID(externalID)
	switch {
	case held == nil:
		return contracts.ExternalAccount{}, errFakeNoSuchAccount
	case held.unconfirmed:
		return contracts.ExternalAccount{}, contracts.ErrExternalUnconfirmed
	}
	held.write(spec)
	return f.answer(held), nil
}

// Delete marks a disabled account deleted, ErrExternalEnabled for an enabled
// one. A deleted account is deleted again without a word.
func (f *FakeIdentityProvisioner) Delete(_ context.Context, actor contracts.Actor, externalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Delete", actor, externalID, nil, true); err != nil {
		return err
	}
	held := f.byID(externalID)
	switch {
	case held == nil:
		return errFakeNoSuchAccount
	case held.unconfirmed:
		return contracts.ErrExternalUnconfirmed
	case held.account.State == "enabled":
		return contracts.ErrExternalEnabled
	}
	held.account.State = "deleted"
	held.pass = nil
	return nil
}

// Account answers the account, or nil when the directory holds none by that
// id (Gone, or never made).
func (f *FakeIdentityProvisioner) Account(_ context.Context, externalID string) (*contracts.ExternalAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["Account"]; err != nil {
		return nil, err
	}
	held := f.byID(externalID)
	if held == nil {
		return nil, nil
	}
	out := f.answer(held)
	return &out, nil
}

// RevealPass answers the account's pass once: a second call, a call after
// its lifetime and a call for an account with none answer ErrPassGone.
func (f *FakeIdentityProvisioner) RevealPass(_ context.Context, actor contracts.Actor, externalID string) (contracts.Pass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("RevealPass", actor, externalID, nil, true); err != nil {
		return contracts.Pass{}, err
	}
	held := f.byID(externalID)
	if held == nil || !f.livePass(held) {
		return contracts.Pass{}, contracts.ErrPassGone
	}
	held.pass.revealed = true
	return contracts.Pass{Value: held.pass.value, ExpiresAt: held.pass.expiresAt}, nil
}

// GroupTraits declares two traits: the group's type, a required choice of
// security or microsoft365, and its mail nickname, a string of at most 64
// characters — a required choice and a bounded string for Staff's form and
// its validation. A read: unrecorded, it answers what FailWith gave it.
func (f *FakeIdentityProvisioner) GroupTraits(context.Context) ([]contracts.GroupTrait, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["GroupTraits"]; err != nil {
		return nil, err
	}
	return []contracts.GroupTrait{
		{
			Key:   "type",
			Type:  "choice",
			Label: map[string]string{"en": "Type", "nb": "Type"},
			Choices: []contracts.TraitChoice{
				{Value: "security", Label: map[string]string{"en": "Security", "nb": "Sikkerhet"}},
				{Value: "microsoft365", Label: map[string]string{"en": "Microsoft 365", "nb": "Microsoft 365"}},
			},
			Default:  "security",
			Required: true,
		},
		{
			Key:       "mailNickname",
			Type:      "string",
			Label:     map[string]string{"en": "Mail nickname", "nb": "E-postalias"},
			MaxLength: fakeNicknameMax,
		},
	}, nil
}

// EnsureGroup validates the spec's values against the declaration — "type"
// missing is trait_required, a type not among the choices or a nickname
// longer than 64 characters trait_invalid, a type differing from the one the
// group was made with trait_immutable, each a *contracts.GroupTraitError
// and nothing written — then records the spec and its owners under the
// caller's id and answers the group's external id, Created on the first
// call for the id.
func (f *FakeIdentityProvisioner) EnsureGroup(_ context.Context, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("EnsureGroup", actor, "", nil, true); err != nil {
		return contracts.ExternalGroup{}, err
	}
	if f.unconfirmedGroups[spec.GroupID] {
		return contracts.ExternalGroup{}, contracts.ErrGroupUnconfirmed
	}
	kind, err := fakeGroupValues(spec.Values)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	group, ok := f.groups[spec.GroupID]
	if ok && group.kind != kind {
		return contracts.ExternalGroup{}, &contracts.GroupTraitError{Field: "type", Code: contracts.TraitImmutable}
	}
	if !ok {
		group = &fakeExternalGroup{externalID: uuid.NewString(), kind: kind}
		f.groups[spec.GroupID] = group
	}
	group.spec = spec
	group.spec.Values = maps.Clone(spec.Values)
	group.spec.OwnerExternalIDs = slices.Clone(spec.OwnerExternalIDs)
	group.owners = slices.Clone(spec.OwnerExternalIDs)
	return contracts.ExternalGroup{ExternalID: group.externalID, Created: !ok}, nil
}

// fakeGroupValues checks values against the fake's declaration and answers
// the type they name, or the *contracts.GroupTraitError of the first value
// it does not admit.
func fakeGroupValues(values map[string]any) (string, error) {
	raw, present := values["type"]
	kind, isString := raw.(string)
	switch {
	case !present || raw == nil || (isString && kind == ""):
		return "", &contracts.GroupTraitError{Field: "type", Code: contracts.TraitRequired}
	case !isString || (kind != "security" && kind != "microsoft365"):
		return "", &contracts.GroupTraitError{Field: "type", Code: contracts.TraitInvalid}
	}
	if raw, present := values["mailNickname"]; present && raw != nil {
		nickname, ok := raw.(string)
		if !ok || utf8.RuneCountInString(nickname) > fakeNicknameMax {
			return "", &contracts.GroupTraitError{Field: "mailNickname", Code: contracts.TraitInvalid}
		}
	}
	return kind, nil
}

// SetMembers makes the group's members exactly memberExternalIDs and answers
// how many it added and removed; a member FailMembers named is reported in
// Failed with its code instead, its membership left as it was.
// ErrGroupNotManageable for a group the fake has not ensured.
func (f *FakeIdentityProvisioner) SetMembers(_ context.Context, actor contracts.Actor, groupID string, memberExternalIDs []string) (contracts.MembersResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SetMembers", actor, groupID, nil, true); err != nil {
		return contracts.MembersResult{}, err
	}
	if f.unconfirmedGroups[groupID] {
		return contracts.MembersResult{}, contracts.ErrGroupUnconfirmed
	}
	group, ok := f.groups[groupID]
	if !ok {
		return contracts.MembersResult{}, contracts.ErrGroupNotManageable
	}
	failures := f.memberFailures[groupID]
	delete(f.memberFailures, groupID)
	var out contracts.MembersResult
	var members []string
	for _, id := range group.members {
		switch {
		case slices.Contains(memberExternalIDs, id):
			members = append(members, id)
		case failures[id] != "":
			out.Failed = append(out.Failed, contracts.MemberFailure{ID: id, Code: failures[id]})
			members = append(members, id)
		default:
			out.Removed++
		}
	}
	for _, id := range memberExternalIDs {
		switch {
		case slices.Contains(members, id):
		case failures[id] != "":
			out.Failed = append(out.Failed, contracts.MemberFailure{ID: id, Code: failures[id]})
		default:
			out.Added++
			members = append(members, id)
		}
	}
	group.members = members
	return out, nil
}

// DeleteGroup forgets the group — an unconfirmed adoption's too, the mark
// with it; one the fake does not hold is deleted without a word.
func (f *FakeIdentityProvisioner) DeleteGroup(_ context.Context, actor contracts.Actor, groupID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("DeleteGroup", actor, groupID, nil, true); err != nil {
		return err
	}
	delete(f.groups, groupID)
	delete(f.unconfirmedGroups, groupID)
	return nil
}

// begin opens a write under the fake's lock: an invalid actor is refused
// unrecorded, as a provider refuses it before any write; otherwise the call
// is recorded, then answers a failure a test shaped, then — for a write
// that needs a connection — ErrProviderUnavailable while the status is not
// connected.
func (f *FakeIdentityProvisioner) begin(method string, actor contracts.Actor, externalID string, spec *contracts.ExternalSpec, needsConnection bool) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	call := ProvisionerCall{Method: method, Actor: actor.System, ExternalID: externalID}
	if actor.UserID != nil {
		call.Actor = actor.UserID.String()
	}
	if spec != nil {
		copied := copySpec(*spec)
		call.Spec = &copied
	}
	f.calls = append(f.calls, call)
	if err := f.fail[method]; err != nil {
		return err
	}
	if needsConnection && f.status != contracts.ProviderConnected {
		return contracts.ErrProviderUnavailable
	}
	return nil
}

// byID is the account holding externalID, nil when none does. The caller
// holds the lock.
func (f *FakeIdentityProvisioner) byID(externalID string) *fakeExternalAccount {
	for _, held := range f.accounts {
		for _, a := range held {
			if a.account.ExternalID == externalID {
				return a
			}
		}
	}
	return nil
}

// livePass reports whether a's pass exists, is unrevealed and has not
// expired on the fake's clock. The caller holds the lock.
func (f *FakeIdentityProvisioner) livePass(a *fakeExternalAccount) bool {
	return a.pass != nil && !a.pass.revealed && f.now().Before(a.pass.expiresAt)
}

// answer is a's account as the fake answers it: HasPass from the pass,
// Created false, Reported copied. The caller holds the lock.
func (f *FakeIdentityProvisioner) answer(a *fakeExternalAccount) contracts.ExternalAccount {
	out := a.account
	out.Created = false
	out.HasPass = f.livePass(a)
	out.Reported = slices.Clone(a.account.Reported)
	return out
}

// addressTaken reports whether another account than self holds address,
// compared without case. The caller holds the lock.
func (f *FakeIdentityProvisioner) addressTaken(address string, self *fakeExternalAccount) bool {
	if address == "" {
		return false
	}
	for _, held := range f.accounts {
		for _, a := range held {
			if a != self && a.account.State != "deleted" && strings.EqualFold(a.account.Email, address) {
				return true
			}
		}
	}
	return false
}

// freeUPN is <given>.<family>@fake.example folded to ASCII lower case, with
// a number after the names when another account holds it. The caller holds
// the lock.
func (f *FakeIdentityProvisioner) freeUPN(given, family string) string {
	local := asciiFold(given) + "." + asciiFold(family)
	for n := 1; ; n++ {
		upn := local + "@fake.example"
		if n > 1 {
			upn = local + strconv.Itoa(n) + "@fake.example"
		}
		taken := false
		for _, held := range f.accounts {
			for _, a := range held {
				if strings.EqualFold(a.account.UserPrincipalName, upn) {
					taken = true
				}
			}
		}
		if !taken {
			return upn
		}
	}
}

// write records spec as the one last written to a.
func (a *fakeExternalAccount) write(spec contracts.ExternalSpec) {
	copied := copySpec(spec)
	a.spec = &copied
}

// asciiFold is s lower-cased with its letters folded to ASCII — the
// Norwegian æ, ø and å to ae, o and a, an accent dropped — and everything
// but a–z, 0–9 and the hyphen left out; "user" when nothing is left.
func asciiFold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case r == 'æ':
			b.WriteString("ae")
		case r == 'ø':
			b.WriteByte('o')
		case r == 'ß':
			b.WriteString("ss")
		case unicode.Is(unicode.Mn, r):
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "user"
	}
	return b.String()
}

// fakePassValue is a random pass, as a directory would issue one.
func fakePassValue() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// copySpec is spec with its map, slices and dates copied, so neither the
// caller nor a test reading Calls writes through to what the fake keeps.
func copySpec(spec contracts.ExternalSpec) contracts.ExternalSpec {
	if spec.HireDate != nil {
		d := *spec.HireDate
		spec.HireDate = &d
	}
	if spec.LeaveDate != nil {
		d := *spec.LeaveDate
		spec.LeaveDate = &d
	}
	spec.Fields = maps.Clone(spec.Fields)
	spec.Licences = slices.Clone(spec.Licences)
	return spec
}

# Entra — connecting existing users (phase 2, design)

Phase 1 of the [Entra module](2026-10-19-entra-design.md) made the tenant follow the
register: an account Staff announces is adopted on arrival, and a person hired after the
module was turned on never meets an Owner. A company that turns single sign-on on
**later**, with a workspace full of users who already sign in with a password, gets
nothing of that. Their next SSO sign-in is refused `oidc_email_conflict`: an address
never links a federated identity to an existing account (the takeover guard,
`srv/identity/oidc.go:545-573`), and nothing but the takeover guard's one exception —
the pending, credential-less user an announcement names to replace — attaches an
arrival to a user who exists.

This phase gives an Owner the authorised exception: a user list that says, per user,
whether they are connected to the provider, and a way to connect one who is not, so that
the connection ends up exactly as a regular SSO sign-in or SCIM creation would have left
it. Code references are against `main` at `df9bf81d`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, "E" the phase 1 design's decisions.

## Readings

1. **A link cannot be written from directory data.** `identity.oidc_links` is keyed on
   `(issuer, subject)` and the subject is the token's `sub` claim
   (`srv/identity/oidc_claims.go:26`, `oidc.go:604`). For Entra `sub` is pairwise per
   application and Graph never returns it. SCIM's `identity.scim_user_mappings` is the
   provider's push, and nothing but a push creates one. So an Owner's mapping can never
   *be* the link; it can only **announce** one, fulfilled by the user's next arrival —
   which is what E's D4 announced account already is for a user who does not exist yet.
2. **The announced account is Staff-shaped.** `identity.expected_accounts` is keyed on
   the employee number, carries a display name and a language for the user it will
   create, and admits a user to replace only when that user is pending and without a
   credential (`adoptable`, `srv/identity/expected_accounts.go:354`). Most of the users
   this phase connects hold no employee number and all of them hold a credential. The
   announcement of a **link** is a different thing, keyed on the user, and gets its own
   table rather than a nullable number and a relaxed `adoptable`.
3. **The arrival's keys are the same.** An OIDC sign-in carries `oid` under the issuer;
   a SCIM create carries `userName` (Entra's default: the user principal name) and
   `externalId` (the mail nickname by default, the object id when the Owner maps it so;
   E's D4). A link announcement therefore names the object id, the user principal name,
   or both — the same two keys an account announcement names — and is matched the same
   way: OIDC on `oid` under the issuer, SCIM on `userName` then `externalId`.
4. **The users list already knows "connected".** `ListManagedUsers` computes
   `sso_enabled` as a link under the configured issuer (`srv/identity/queries/owner_users.sql:21-24`),
   and the Users screen counts it. What the screen lacks is the state between an
   Owner's act and the arrival, and the act itself.
5. **An Owner's act is the authority.** Everything else that attaches an arrival to an
   existing user is guarded against a takeover by address. An Owner naming a user and a
   directory account is the one actor entitled to say they are the same person; the
   arrival's email claim is then not consulted for that user. Owner endpoints already
   run under MFA when `OWNERS_REQUIRE_MFA` holds, and every user write runs under the
   owner lock in a serializable transaction (`srv/identity/users.go:57-72`).
6. **Nothing unlinks today.** `oidc_links` has no delete; the OpenAPI text on an
   authority change says relinking waits for "a later release" (`openapi/identity.yaml:1918`).
   A mapping feature without an undo makes a wrong mapping permanent, and the authority
   change's orphaned links finally get their remedy: unlink, then announce under the new
   issuer.
7. **The Graph client finds by id and by employee number only** (`srv/entra/lifecycle_user.go:146-165`).
   Searching the directory by name or address needs `$search` with `ConsistencyLevel: eventual`,
   or `$filter startswith`, neither of which the module has — and the module is the
   only code that may talk to Graph (boundaries rule 1). The picker that queries the
   directory is the Entra module's, and the identity side of this phase must stand
   without it, with the Owner typing the user principal name.

## Decisions

### L1 — `identity.expected_links`: the announced link, one per user

Migration `mig/00064_identity_expected_links.sql`:

```sql
CREATE TABLE identity.expected_links (
    user_id              uuid         PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    issuer               varchar(200) NOT NULL,
    object_id            varchar(200),
    user_principal_name  varchar(254),
    created_at           timestamptz  NOT NULL,
    created_by_user_id   uuid         NOT NULL,
    CONSTRAINT expected_links_key_check CHECK (object_id IS NOT NULL OR user_principal_name IS NOT NULL)
);
CREATE UNIQUE INDEX ux_expected_links_object_id ON identity.expected_links (issuer, object_id) WHERE object_id IS NOT NULL;
CREATE UNIQUE INDEX ux_expected_links_upn ON identity.expected_links (user_principal_name) WHERE user_principal_name IS NOT NULL;
```

A user has at most one announced link (the primary key); a directory account is
announced for at most one user under an issuer (the two unique indexes, both scoped by
the issuer — a second Owner naming the same account for another user is refused, never
silently the newest; an announcement left under an old issuer blocks nothing). The issuer is
always set: it is the configured OIDC authority, normalised, at the moment of the
announcement, and a change of authority makes the announcement unfulfillable (L4), as
it does the links. The object id is stored as SCIM's `externalId` is normalised
(`normalizeExternalID`: a GUID in lower-case dashed form) so the two compare equal; the
principal name as emails are normalised. The actor is always a user: only an Owner
announces a link (L2). `Down` drops the table: an unfulfilled announcement is made
again by the Owner.

### L2 — Two Owner endpoints: announce, and unlink

Under `/api/v1/identity/owner/users/{id}`, Owner-only, MFA-gated as the rest, the self
check applied (an Owner connects their own account through the sign-in, not here):

**`POST …/sso-link`** with `{ "userPrincipalName"?: string, "objectId"?: string }` records
or replaces the user's announced link, in one serializable transaction under the owner
lock. Judged in this order:

| Answer | When |
| --- | --- |
| 400 `invalid_request` | neither key; a principal name that is not an address or longer than 254; an object id longer than 200 or holding a control character; the user is the caller |
| 404 | no such user |
| 409 `sso_not_configured` | OIDC is off: there is no issuer to announce under |
| 409 `account_pending` | the user is pending: their way in is the invitation, or the announced account that replaces them (E's D4) |
| 409 `account_linked` | the user already has a link under the configured issuer |
| 409 `sso_link_taken` | the object id under the issuer, or the principal name, is announced for another user — or named by an account announcement (`expected_accounts`), which Staff made for a user yet to arrive |

A disabled user may be announced: the arrival signs in by the linked account's rules
and is `account_locked` until enabled. An Owner or a SystemAdmin may be announced: SSO
is how they sign in too, and the local MFA rule stands (`oidcSignInLinked`). The same
announcement again changes nothing; any change replaces it whole. Audited
`account.link-expected` on the user with `keys` (`objectId`, `userPrincipalName`) — never
the address.

**`DELETE …/sso-link`** removes, in one transaction under the owner lock, the user's
announced link when there is one (audited `account.link-unexpected`) and every
`oidc_links` row of the user under the configured issuer (audited `account.unlinked`
with `links`, the count). Nothing to remove is 200 with the user unchanged. A SCIM
mapping is **not** touched: it is the provider's record and ends with the provider's
delete. A user left without any credential keeps their account and signs in again only
through a reset or a new announcement; the screen says so before the Owner confirms.

Both answer the `OwnerUserResponse`, which gains `ssoLink`:

```yaml
ssoLink:
  type: object
  description: The user's connection to the workforce OIDC provider. Absent while OIDC is off.
  properties:
    state: { enum: [linked, expected, none] }
    userPrincipalName: { type: string }   # the announced key, while expected
    objectId:          { type: string }   # the announced key, while expected
    expectedAt:        { format: date-time }
```

`ssoEnabled` stays, equal to `ssoLink.state == linked`.

### L3 — The arrival fulfils the announced link before anything else

**OIDC** (`GetIdentityOidcComplete`, step 3b): with no link for `(issuer, sub)`, the
announced link under the token's issuer is read by the first key that names one: the
token's `oid`, then the token's **principal name** — Entra's `preferred_username`, the
user principal name, when it is an address, else the verified `email` claim (Google has
no `oid` and no principal name; its address is verified under the hosted domain) —
compared with the announced principal name as emails are compared. So an announcement
by principal name alone, the modal's default, is fulfilled by a sign-in too. When one is
found, and its user still exists and holds no link under the issuer, the completion
runs — in the same serializable transaction under the owner lock that provisioning
uses, entered only after the announcement is found outside it, so a sign-in nobody
announced never waits for the lock — `InsertOidcLink(issuer, sub, user)`, deletes the
announcement, audits `account.linked-from-provider` with `matchedBy` `oid` or
`principalName`, and signs the user in as a linked account (`oidcSignInLinked`: disabled is `account_locked`, TOTP is
`local_mfa_required`). **The email claim is not consulted**: the Owner named the user
(reading 5). The user's address, display name and roles are kept as they are — the
provider's claims never became roles for a provisioned user either. An announced link
whose user has meanwhile gained a link under the issuer is no match, and the arrival
proceeds as it does today. A fulfilment that loses its serializable transaction on
every attempt re-reads the link and, finding none, answers `oidc_sign_in_unavailable`
rather than falling through to provision a second user beside the announced one. The announced link is read **before** the announced account
(`oidcExpected`): one names a user who exists, the other a user who does not, and the
Owner's act is the more specific.

**SCIM** (`PostIdentityScimV2Users`, before `adoptExpectedScimUser`): the announced link
whose principal name equals `userName` (normalised), else whose object id equals
`externalId`, is read. When one is found and its user holds no SCIM mapping, the create
maps the resource to that user (`insertScimMapping`) instead of inserting a user:
`upstream_active`, the profile and the ETag as a create sets them. The user's address
becomes the resource's work email when it carries one that no other user holds
(another user's is `DuplicateEmail`, as a create answers it; none keeps the address),
since a mapped user's profile is the provider's from here on; the display name is kept,
and `active` is the mapping's `upstream_active`, as for any mapped user. The announcement is deleted and the mapping
audited `account.linked-from-provider` with `matchedBy` `userName` or `externalId`,
beside `scim.user.created`. A user who already has a mapping is no match: the arrival
is a plain create, and `DuplicateEmail` says what it says.

An announced link outranks an announced account on both paths; an arrival never
fulfils both.

### L4 — What an authority change and a turn-off do

`refuseWhileLinked` (`srv/identity/sso_settings_owner.go:483`) counts the links under
the current issuer; it now counts the announced links too, so `oidc_links_exist` covers
both and the confirmation dialog's words stay true. Confirmed, the change leaves the
announcements behind as it leaves the links: their issuer no longer matches, they
fulfil nothing, and the Users screen lists the user as not connected. The remedy needs
no disconnect — a link under the old issuer counts for nothing, since `sso_enabled` and
L2's `account_linked` both read the configured issuer only — so the Owner announces each
person's account again under the new provider, and their next sign-in links them under
it; the old row stays, read by nothing. The OpenAPI text that promised relinking to a
later release now says how.

### L5 — The Users screen

Workspace administration → Users (`apps/host/frontend/src/routes/workspace/users.tsx`)
gains a **Single sign-on** column, shown while OIDC is on (the Owner SSO settings the
screen's card already reads), on the desktop table and the mobile card alike:

| `ssoLink.state` | The cell | The menu |
| --- | --- | --- |
| `linked` | a green check and *Connected* | *Disconnect from single sign-on…* (confirm: "They keep their account and sign in with a local credential, if they have one.") |
| `expected` | a blue *Awaiting first sign-in* badge with the announced principal name or object id beneath | *Change the connection…*, *Cancel the connection* |
| `none` | the button *Connect…* | — |

*Connect…* opens a modal titled with the user's name: the user principal name (an
address, pre-filled with the user's own), and under *More*, the object id. A pending
user shows no button: their way in is the invitation. It explains, in a sentence, that the
connection completes the first time the person signs in through the provider, or the
provider pushes the account. The refusals of L2 are said in the Owner's words
(`sso_link_taken`: "Another user is already connected to this account."). The *SSO*
metric card keeps counting the linked. A disabled or locked user is connected the same
way. The self row shows no button.

The Entra module's picker — searching the tenant by name or address and suggesting
exact matches — replaces the typed principal name in delivery B (below); the modal is
built so a picker drops into it.

### L6 — Out of this phase

- **Staff's `entra` kind for a person whose user has a credential.** HR changing a
  person's kind from `local` to `entra` while the identity link is active (not pending)
  leaves Staff's `Expect` with nothing to replace (E's D4), and the person's sign-in
  refused as before. Whether Staff should announce a *link* for an active user — the
  provider account it created or adopted for them — is Staff's decision table's to
  take, with the account kind's rules; it is noted for Staff phase 2 and not changed
  here. An Owner connects such a user by hand with L2 meanwhile.
- **The password after the connection.** It stays. The authority mode and Staff's
  account kind decide what a local credential may do, not the link; a company that
  wants SSO-only switches the mode, as before.
- **Google as a provider.** L1–L5 are provider-neutral (`oid` is Entra's; the
  object-id key is simply unused for a provider without one, and the principal name
  carries it). The picker of delivery B is Entra's.
- **A user with several links.** One user, one link under an issuer, as today.

## Deliveries

Two pull requests, each against `main` after the previous lands, one migration each at
most:

**A — The announced link** (`feat/identity-expected-links`): L1, L2, L3, L4, L5 with
the typed principal name; migration `00064_identity_expected_links.sql`; the OpenAPI
contract; `user/workspace-administration.md`, `admin/sso-scim.md` and
`admin/authentication.md` in both languages; `ROADMAP.md`'s Entra phase 2.

**B — The directory picker** (`feat/entra-directory-picker`): in the Entra module, a
Graph search (`/users?$search=…` with `ConsistencyLevel: eventual`, `$select` the
display name, mail, user principal name, object id, `employeeId`, `accountEnabled`)
behind `GET /api/v1/entra/directory/users?q=` for an Owner, and
`GET /api/v1/entra/directory/matches` proposing, for every unconnected user, the
directory account whose mail or principal name equals their address or whose
`employeeId` equals their employee number — exact matches only; the picker in L5's
modal, which shows each hit's `employeeId`, enabled state and whether it is announced
or linked already; a review list with *Connect selected*; `user/entra.md` and
`reference/entra.md` in both languages. No migration.

## Testing

**A.** `srv/identity`: `POST …/sso-link` for each refusal in L2's order and the no-op
repeat; the OIDC arrival that fulfils an announced link (signed in as linked, the email
claim another user's and ignored, the audit's `matchedBy`), that finds the user linked
meanwhile (no match, `oidc_email_conflict` as before), and that finds an announced
account for the same `oid` (the link wins); the SCIM create that fulfils one by
`userName` and by `externalId`, with the work email taken, with another user's work
email (`DuplicateEmail`) and with none; `DELETE …/sso-link` on a linked user, an
announced user and a plain user; `refuseWhileLinked` counting announcements. The
frontend's `users.test.tsx`: the column's three states, the modal's refusals, the
disconnect confirmation. The docs gate.

**B.** The Graph fake answering `$search`; the matches endpoint's exact-only rule; the
picker's flow in the host's test.

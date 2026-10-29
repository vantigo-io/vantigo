// Package config loads and validates the process configuration from
// environment variables. It is parsed once at startup and reports every
// problem at once, so a misconfigured container fails its first boot with the
// complete list instead of one restart per mistake.
//
// Add new settings here, never by reading os.Getenv at a call site.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// Env is the deployment environment. Only development relaxes anything.
type Env string

const (
	Production  Env = "production"
	Development Env = "development"
)

// Branding is the whitelabeling the SPA index document is templated with.
type Branding struct {
	Title        string
	LogoURL      string
	SupportEmail string
	SupportPhone string
	SupportURL   string
}

// SessionConfig bounds the application cookie session. The idle timeout is
// what the cookie itself enforces; the absolute lifetime is enforced on every
// validation so an actively used session cannot renew indefinitely.
// Privileged sessions (Owner, SystemAdmin) get the tighter pair of bounds.
type SessionConfig struct {
	Idle               time.Duration
	PrivilegedIdle     time.Duration
	Absolute           time.Duration
	PrivilegedAbsolute time.Duration
}

// MailConfig is where application mail (invitations, password resets) is
// sent. Driver "log" writes mail to the log instead of sending it, and is
// rejected outside development because those mails carry bearer links.
type MailConfig struct {
	Driver   string // "smtp" | "log"
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string // "implicit" | "starttls" | "none"
}

// OIDCConfig is the one startup-bound workforce OpenID Connect provider. A
// nil *OIDCConfig on Config means OIDC is disabled.
type OIDCConfig struct {
	Provider          string // "entra" | "google"
	Authority         string
	ClientID          string
	ClientSecret      string
	WorkloadTokenFile string
	AllowedDomains    []string
	DisplayName       string
}

// SCIMConfig is the static bearer credential for the SCIM endpoint. A nil
// *SCIMConfig on Config means SCIM is disabled. PreviousToken lets a token
// rotation overlap briefly instead of breaking every in-flight client at once.
type SCIMConfig struct {
	Token                  string
	PreviousToken          string
	PreviousTokenExpiresAt time.Time
}

// ManagementConfig enables the management listener: a second, private HTTP
// listener a control plane polls. A nil *ManagementConfig on Config means it
// is disabled. It is bearer-authenticated and has no host filter, so it must
// never be published or routed from the internet.
type ManagementConfig struct {
	Port  int
	Token string
}

// Config is the validated process configuration. Load populates every field;
// there is no partial state.
type Config struct {
	Env Env
	// DatabaseURL is the runtime connection (the least-privilege role).
	DatabaseURL string
	// MigrationsDatabaseURL is the connection `migrate` uses: the owner role
	// where a deployment separates the two, otherwise DatabaseURL.
	MigrationsDatabaseURL string
	// AppOrigin is APP_URL as scheme://host[:port], lower-case, no trailing slash.
	AppOrigin string
	// AppHostname is AppOrigin's host without port or IPv6 brackets.
	AppHostname string
	// BasePath is "" at the domain root, otherwise "/prefix" with no trailing slash.
	BasePath string
	// PublicBaseURL is PUBLIC_BASE_URL (payments port design D14): the https
	// URL the pay page is reachable at from the internet — PublicOrigin
	// followed by BasePath, no trailing slash — or "" when unset, and with it
	// pay links and webhooks are unavailable. Its path is BasePath, because
	// the SPA's index is rendered once for that base path and StripBasePath
	// strips only it.
	PublicBaseURL string
	// PublicOrigin is PublicBaseURL's scheme://host[:port], lower-case.
	PublicOrigin string
	// PublicHostname is PublicOrigin's host without port or IPv6 brackets.
	// When it differs from AppHostname the host filter admits it too and, on
	// it, the public host gate serves only the pay page's allowlist. It is
	// never AppHostname on another port: both compare host names without the
	// port, so the two could never be told apart.
	PublicHostname   string
	Port             int
	TrustedProxyHops int
	CSPReportOnly    bool
	ShutdownTimeout  time.Duration
	LogLevel         slog.Level
	Branding         Branding

	// AppSecret is the process-wide key material (CSRF tokens, cookie
	// signing, encryption of TOTP secrets). At least 32 bytes.
	AppSecret []byte
	Sessions  SessionConfig

	OwnersRequireMFA         bool
	OwnersAllowInsecureNoMFA bool
	MFAIssuer                string

	InvitationLifetime  time.Duration
	InvitationAcceptURL string
	PasswordResetURL    string

	Mail MailConfig
	OIDC *OIDCConfig
	SCIM *SCIMConfig
	// WorkloadTokenDir is OIDC_WORKLOAD_IDENTITY_TOKEN_DIR, cleaned: the
	// directory a workload identity token file saved on the Single sign-on
	// card must lie in. An Owner names the file; the deployment bounds
	// where on the filesystem the server will stat, open and read on their
	// word. See workloadTokenDir for the default.
	WorkloadTokenDir string

	// BootstrapOwnerEmail, when set, replaces the anonymous /setup bootstrap:
	// POST /identity/bootstrap is disabled and, while no Owner exists, startup
	// mails an Owner invitation to this address (BOOTSTRAP_OWNER_EMAIL).
	BootstrapOwnerEmail string

	// Management is the private management listener (MANAGEMENT_PORT and
	// MANAGEMENT_TOKEN, set together). Nil disables it.
	Management *ManagementConfig

	TrustedProxyCIDRs []netip.Prefix

	// Modules are the business modules MODULES enables (trimmed, lower-cased
	// names from the known set). Identity is always mounted and never
	// appears here; module.Compose mounts only the modules this list enables
	// plus identity.
	Modules []string

	// WorkersInProcess is WORKERS_IN_PROCESS: whether api mode also runs
	// every enabled module's background workers in-process, alongside
	// serving. worker mode always runs them and server mode never does,
	// regardless of this setting (design §3.2). Defaults on, for the
	// single-container deployment shape; a deployment that splits api and
	// worker into separate replicas sets it to 0 on the api ones.
	WorkersInProcess bool

	// BrregBaseURL is the origin the customers module's Brreg lookup client
	// calls (BRREG_BASE_URL, customers inventory §5, CFG/BrregLookupOptions.cs:11);
	// no trailing slash. The module's only outbound HTTP dependency.
	BrregBaseURL string
	// BrregTimeout bounds one lookup end to end, retries included
	// (BRREG_TIMEOUT); .NET's total-request timeout.
	BrregTimeout time.Duration

	// PeppolLookupEnabled is the on/off switch for the customers module's
	// Peppol lookup (PEPPOL_LOOKUP_ENABLED, can-this-customer-receive-EHF
	// design D3, D5), default on. false answers the operation 503 without
	// ever consulting Deps.PeppolLookup or building the real client.
	PeppolLookupEnabled bool
	// PeppolSMLZone is the SML zone a lookup hashes a participant identifier
	// into (PEPPOL_SML_ZONE, internal/peppol.Options.Zone's own doc: "a
	// hard-coded default in this package would be a second place for it to
	// be wrong"), default the production network
	// (participant.sml.prod.tech.peppol.org); the test network is
	// participant.sml.test.tech.peppol.org.
	PeppolSMLZone string
	// PeppolDNSServer is the host:port of a resolver the Peppol client uses
	// instead of the server's own name servers (PEPPOL_DNS_SERVER), empty by
	// default (meaning /etc/resolv.conf).
	PeppolDNSServer string
	// PeppolTimeout bounds one Peppol lookup end to end, DNS and SMP together
	// (PEPPOL_TIMEOUT), default 10s.
	PeppolTimeout time.Duration

	// CustomersRegistryFeedEnabled is the on/off switch for the customers
	// module's Brreg update-feed worker (CUSTOMERS_REGISTRY_FEED_ENABLED,
	// registry workers design D7). Default ON: the worker is what keeps a
	// registry record, the dashboard's attention list and the billing warnings
	// from going quietly stale, so an installation that says nothing gets it.
	// A 0 means the worker is never handed to the runner at all, so no
	// scheduled request is made to Brreg.
	CustomersRegistryFeedEnabled bool
	// CustomersRegistryFeedPoll is how often that worker runs a cycle
	// (CUSTOMERS_REGISTRY_FEED_POLL, default 15 minutes). The feed is one small
	// request per poll, so this is about how soon a change is noticed rather
	// than about load.
	CustomersRegistryFeedPoll time.Duration
	// CustomersPeppolRecheckEnabled is the on/off switch for the Peppol
	// re-check worker (CUSTOMERS_PEPPOL_RECHECK_ENABLED, design D6, D7).
	// Effective only alongside PeppolLookupEnabled: with the lookup itself off
	// there is no network to ask, and the worker is not started either way.
	CustomersPeppolRecheckEnabled bool
	// CustomersPeppolRecheckPoll is how often that worker runs a cycle
	// (CUSTOMERS_PEPPOL_RECHECK_POLL, default 24 hours). A Peppol registration
	// is not a thing that changes hourly, and each cycle is up to a hundred
	// network lookups.
	CustomersPeppolRecheckPoll time.Duration
	// CustomersPeppolRecheckAge is how old a stored lookup must be before the
	// worker asks again (CUSTOMERS_PEPPOL_RECHECK_AGE, default 720 hours — 30
	// days). It is deliberately a duration rather than a day count, so an
	// installation can shorten it to something a test or a pilot can observe.
	CustomersPeppolRecheckAge time.Duration
	// CustomersAnonymisationEnabled is the on/off switch for the customers
	// module's anonymisation worker (CUSTOMERS_ANONYMISATION_ENABLED,
	// customers GDPR design D4). Default ON, like the registry workers, and for
	// a stronger reason: the worker does what a person scheduled for a date, and
	// an installation that says nothing must still keep that promise. A 0 means
	// the worker is never handed to the runner, and scheduled dates simply wait.
	CustomersAnonymisationEnabled bool
	// CustomersAnonymisationPoll is how often that worker runs a cycle
	// (CUSTOMERS_ANONYMISATION_POLL, default 24 hours): a schedule is a day, so
	// once a day is on time. Each cycle is at most fifty customers, a constant.
	CustomersAnonymisationPoll time.Duration
	// CustomersImportEnabled is the on/off switch for the customers module's
	// import worker (CUSTOMERS_IMPORT_ENABLED, customers import wizard design
	// D6), the one that checks and runs the files people upload. Default ON:
	// with it off an import queues and waits, and the page says so. A 0 means
	// the worker is never handed to the runner.
	CustomersImportEnabled bool
	// CustomersImportPoll is how often that worker looks for a queued import
	// (CUSTOMERS_IMPORT_POLL, default 5 seconds — the outbox's figure, because a
	// person is watching this one). In the default deployment the handlers also
	// nudge the worker in-process, so the poll is the split deployment's bound.
	CustomersImportPoll time.Duration
	// CustomersImportRetention is how long a finished or abandoned import and
	// its rows are kept before the worker's sweep removes them
	// (CUSTOMERS_IMPORT_RETENTION, default 720 hours — 30 days): the rows are
	// personal data in flight, not a record to keep.
	CustomersImportRetention time.Duration

	// EnergyImportEnabled is the on/off switch for the energy module's import
	// worker (ENERGY_IMPORT_ENABLED, energy import design D6), the one that
	// checks, runs and attaches the metering-point files people upload.
	// Default ON, as the customers import's: with it off an import queues and
	// waits, and the page says so. A 0 means the worker is never handed to
	// the runner.
	EnergyImportEnabled bool
	// EnergyImportPoll is how often that worker looks for a queued import
	// (ENERGY_IMPORT_POLL, default 5 seconds, the customers import's figure:
	// a person is watching). The handlers also nudge the worker in-process,
	// so the poll is the split deployment's bound.
	EnergyImportPoll time.Duration
	// EnergyImportRetention is how long a finished or abandoned import and
	// its rows are kept before the worker's sweep removes them
	// (ENERGY_IMPORT_RETENTION, default 720 hours — 30 days).
	EnergyImportRetention time.Duration

	// EnergySpotPriceFeedEnabled is the on/off switch for the energy module's
	// spot price worker (ENERGY_SPOT_PRICE_FEED_ENABLED, energy consumption
	// billing design D2), which fetches each day's day-ahead prices per
	// price area from a public feed. Default OFF: an installation opts in to
	// an outbound call of its own; prices can always be imported from a file.
	EnergySpotPriceFeedEnabled bool
	// EnergySpotPriceFeedURL is the feed's address as a template
	// (ENERGY_SPOT_PRICE_FEED_URL) with {year}, {month}, {day} and {area},
	// rendered per area and civil day with the month and day zero-padded;
	// https only. The default is the public feed hvakosterstrommen.no.
	EnergySpotPriceFeedURL string
	// EnergySpotPriceFeedPoll is how often the worker's cycle runs
	// (ENERGY_SPOT_PRICE_FEED_POLL, default 15 minutes, at least a minute):
	// each cycle fetches the days that are missing, so the poll only bounds
	// how soon tomorrow's prices arrive after they are published.
	EnergySpotPriceFeedPoll time.Duration
	// EnergySpotPriceServiceURL is the shared spot-price service's base URL
	// (ENERGY_SPOT_PRICE_SERVICE_URL, spot-price service design S3),
	// http://vantigo-spotprices:8080 in our hosted environments. Set, it
	// turns the spot price worker on with the service as its source; it and
	// ENERGY_SPOT_PRICE_FEED_ENABLED are never both on. Absolute http or
	// https, no user info, no query or fragment, the path empty or "/". An
	// http URL is dialled only to a private address (design S7).
	EnergySpotPriceServiceURL string
	// EnergySpotPriceServiceKey is the installation's bearer key for the
	// service (ENERGY_SPOT_PRICE_SERVICE_KEY), required with the URL. A
	// secret: redacted wherever the configuration is printed.
	EnergySpotPriceServiceKey string
	// EnergySpotPriceHistoryFrom is the oldest civil day the service sync
	// copies (ENERGY_SPOT_PRICE_HISTORY_FROM, YYYY-MM-DD), the zero time when
	// unset: everything the service holds for an area.
	EnergySpotPriceHistoryFrom time.Time

	// InvoicesEhfEnabled is the on/off switch for sending invoices as EHF over
	// Peppol (INVOICES_EHF_ENABLED, EHF and KID design D1). Default ON, like
	// PEPPOL_LOOKUP_ENABLED: an installation still needs access-point
	// credentials and the seller's Peppol id before anything is sent. A 0
	// turns the feature off: the send answers 503 ehf_unavailable and the EHF
	// workers are never handed to the runner. Effective only alongside
	// PeppolLookupEnabled — a send that cannot re-check its receiver does not
	// send.
	InvoicesEhfEnabled bool
	// InvoicesStorecoveBaseURL is the origin and path the Storecove
	// access-point adapter calls (INVOICES_STORECOVE_BASE_URL, design D1),
	// default the production API; no trailing slash. The operator's, never the
	// tenant's: an operator points it at the sandbox or a mock host, and an
	// invoices:manage holder cannot aim the client anywhere.
	InvoicesStorecoveBaseURL string
	// InvoicesVippsEnabled is the on/off switch for Vipps MobilePay payments
	// (INVOICES_VIPPS_ENABLED, payments port design D1). Default ON, like
	// INVOICES_EHF_ENABLED: an installation still needs the sales unit's
	// credentials, and pay links PUBLIC_BASE_URL, before anything is paid. A 0
	// turns the feature off: every operation that would call the provider
	// answers 503 payments_unavailable and the payment worker is never handed
	// to the runner.
	InvoicesVippsEnabled bool
	// InvoicesVippsBaseURL is the origin the Vipps MobilePay adapter calls
	// (INVOICES_VIPPS_BASE_URL), default the production API; no trailing
	// slash. The operator's, never the tenant's: an operator points it at
	// Vipps' test environment or a mock host.
	InvoicesVippsBaseURL string

	// StaffImportMaxRows is how many people one Staff import takes and one
	// Staff export writes (STAFF_IMPORT_MAX_ROWS, staff records plan Task 3),
	// default 5000 and never fewer — one number for both, so a file the
	// export wrote always fits the import, and never under the five thousand
	// the import template promises. At most 20000: the import's body is
	// capped at 5 MiB, and an export row runs to some 150 to 250 bytes, so
	// past about twenty thousand people the export would outgrow the file the
	// import takes and no longer import back. Every row is also its own
	// transaction, and a run at five thousand already takes most of the
	// minute and a half a proxy in front of a hosted installation allows.
	StaffImportMaxRows int
	// StaffRetentionEnabled is the on/off switch for the Staff module's
	// retention worker, staff-retention (STAFF_RETENTION_ENABLED, staff
	// foundation design S15 and §8.5). Default ON, as the customers
	// anonymisation worker's is and for its reason: the erase is what the law
	// asks of the register, and an installation that says nothing must still
	// keep it. A 0 means the worker is never handed to the runner: nothing is
	// erased on a schedule, and objects queued for deletion wait — POST
	// /retention/run still runs both by hand.
	StaffRetentionEnabled bool
	// StaffAccountsEnabled is the on/off switch for the Staff module's
	// accounts worker, staff-accounts (STAFF_ACCOUNTS_ENABLED, staff
	// foundation design §8.5; accounts plan reading 17). Default ON, as the
	// retention worker's is: nothing happens until HR turns a person's
	// account flag on — from then the person's pending, disabled user is
	// created ahead of the start even before an Owner names the employee
	// roles, and nobody is enabled until they are named. A 0 means the
	// worker is never handed to the runner: no account is prepared, enabled
	// or disabled on a schedule — leavers included — until it is on again.
	StaffAccountsEnabled bool
	// StaffCompetenceEnabled is the on/off switch for the Staff module's
	// competence worker, staff-competence (STAFF_COMPETENCE_ENABLED, staff
	// competence design S7). Default ON, as the other two Staff workers' are:
	// a certificate that lapses unnoticed is what the register is kept to
	// prevent. A 0 means the worker is never handed to the runner: nobody is
	// mailed ahead of a certificate's expiry or on it, while the dashboard's
	// expiring and expired items, computed on read, still show.
	StaffCompetenceEnabled bool
	// StaffWorkersPoll is how often the Staff workers run a cycle
	// (STAFF_WORKERS_POLL, default 1 hour, never under a minute): each poll
	// takes the retention lease for an erase cycle — at most fifty people, a
	// constant — and makes a pass over the deletion ledger; the accounts
	// worker claims at most fifty due links, and retries a failed one at the
	// next poll. The erase is
	// idempotent, so an hourly poll is "once per civil day" kept however the
	// clock changes; a minute is the floor because every poll asks the object
	// store about every due row.
	StaffWorkersPoll time.Duration

	// CalendarCancellationsEnabled is the on/off switch for the calendar
	// module's cancellations worker, calendar-cancellations
	// (CALENDAR_CANCELLATIONS_ENABLED, calendar invitations design D6).
	// Default ON: a cancelled appointment is kept only so its CANCEL can name
	// what it cancels, and an installation that says nothing must still erase
	// it once its last day has passed. A 0 means the worker is never handed to
	// the runner: cancelled appointments stay until it is on again.
	CalendarCancellationsEnabled bool
	// CalendarWorkersPoll is how often the calendar's workers run a cycle
	// (CALENDAR_WORKERS_POLL, default 1 hour, never under a minute): each poll
	// takes the cancellations lease and erases at most two hundred cancelled
	// appointments whose last day has passed — a constant in the worker. The
	// erase is idempotent and nothing waits on it, so an hourly poll is ample;
	// a minute is the floor so a typo is not a hot loop against the database.
	CalendarWorkersPoll time.Duration
	// CalendarSubscriptionsEnabled is the on/off switch for the calendar
	// module's subscriptions worker, calendar-subscriptions
	// (CALENDAR_SUBSCRIPTIONS_ENABLED, calendar feed design D7). Default ON: a
	// person who subscribed to a calendar expects it to follow its source, and
	// an installation that says nothing must still fetch it. A 0 means the
	// worker is never handed to the runner: every subscription shows its last
	// read until it is on again, and a save or Refresh now still fetches once.
	CalendarSubscriptionsEnabled bool
	// CalendarSubscriptionsPoll is how often each subscription is fetched
	// (CALENDAR_SUBSCRIPTIONS_POLL, default 30 minutes, never under five):
	// after an attempt the next is due a poll later, or later still when the
	// feed asks for it with its REFRESH-INTERVAL (capped at a day), plus a
	// jitter of up to a tenth of the poll. The worker itself wakes every five
	// minutes — a constant — so a shorter poll would promise what it cannot
	// keep, and five minutes is the floor.
	CalendarSubscriptionsPoll time.Duration
	// CalendarMailboxSyncEnabled is the on/off switch for the calendar
	// module's Outlook sync worker, calendar-mailbox-sync
	// (CALENDAR_MAILBOX_SYNC_ENABLED, calendar Outlook sync design O7).
	// Default ON: a person who turned the sync on expects their copies kept,
	// and an installation that says nothing must still keep them. A 0 means
	// the worker is never handed to the runner: nothing is written to or
	// removed from anyone's mailbox until it is on again.
	CalendarMailboxSyncEnabled bool
	// CalendarMailboxSyncPoll is how often each person's copies are synced
	// when nothing nudged them (CALENDAR_MAILBOX_SYNC_POLL, default 15
	// minutes, never under one): an appointment write makes its people due at
	// once, so the poll is what catches the providers' items and a change of
	// rights. The worker wakes every minute — a constant — which is the floor.
	CalendarMailboxSyncPoll time.Duration
	// CalendarMailboxNotificationsEnabled is the switch for the change
	// notifications from Outlook (CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED,
	// calendar Outlook sync design O11.1). Default ON: they are asked for
	// only while PUBLIC_BASE_URL is set too, Microsoft needing a public HTTPS
	// address. A 0 is the operator's off switch for a public host Microsoft
	// cannot reach: nothing is subscribed, and the poll alone runs.
	CalendarMailboxNotificationsEnabled bool

	// StorageProvider selects the internal/storage backend: "" (unset,
	// STORAGE_PROVIDER) or "fs". When unset, internal/storage.New still
	// returns a store rather than failing to start; every operation on it
	// reports storage as not configured (docs/src/content/docs/en/admin/object-storage.md, design §2).
	StorageProvider string
	// StorageFSRoot is the fs driver's root directory (STORAGE_FS_ROOT),
	// required and validated as an absolute path only when StorageProvider
	// is "fs".
	StorageFSRoot string
	// StorageFSAllowInsecureRoot relaxes the fs driver's group/world-writable
	// root check (STORAGE_FS_ALLOW_INSECURE_ROOT). Accepted only when Env is
	// Development; set outside development it is a configuration error, the
	// same shape as MAIL_DRIVER=log and OWNERS_ALLOW_INSECURE_NO_MFA.
	StorageFSAllowInsecureRoot bool

	// CommunicationsAttachmentMaxBytes bounds one staged attachment
	// (COMMUNICATIONS_ATTACHMENT_MAX_BYTES), moved here from .NET's removed
	// scanner options (communications design doc D3: "the size limit moves
	// to communications config"). Clamped to [1, 50 MiB], the same bound
	// .NET's own `Math.Clamp(options.MaxBytes, 1, 50 * 1024 * 1024)`
	// (EP/ConversationEndpoints.cs:117) applies to its configured value.
	CommunicationsAttachmentMaxBytes int64
	// CommunicationsUploadExpiry is how long a staged attachment stays valid
	// before the retention worker sweeps it (COMMUNICATIONS_UPLOAD_EXPIRY,
	// communications design doc D3: "expiry moves to the retention worker").
	// Staging sets attachment_uploads.expires_at to now+this value, and
	// communications' RetentionWorker.ExpireStagedUploads is what acts on it.
	CommunicationsUploadExpiry time.Duration
	// CommunicationsInboundMaxBytes is the largest inbound message, in bytes
	// of MIME, the communications-mailbox-sync worker imports
	// (COMMUNICATIONS_INBOUND_MAX_BYTES, default 25 MiB): a larger one is
	// recorded skipped as too_large and never read. Bounded to [1 MiB,
	// 150 MiB], Graph's attachment ceiling (Outlook connection design M14).
	CommunicationsInboundMaxBytes int64

	// CommunicationsRetentionDays is how old terminal communication history
	// must be before the retention worker deletes it
	// (COMMUNICATIONS_RETENTION_DAYS, .NET's Communications:Retention:Days,
	// default 365 — the conservative twelve months the operator guidance
	// recommends; communications inventory §12.1). .NET silently clamps its
	// configured value with max(1, days); this file rejects a value outside
	// [1, 36500] instead, the same treatment
	// COMMUNICATIONS_ATTACHMENT_MAX_BYTES gets, so a typo is a startup error
	// rather than a retention window nobody asked for. The upper bound is
	// this port's own: .NET has none, and a hundred years is well past any
	// real policy.
	CommunicationsRetentionDays int
	// CommunicationsRetentionBatchSize is how many messages one retention
	// batch may delete (COMMUNICATIONS_RETENTION_BATCH_SIZE,
	// Communications:Retention:BatchSize, default 100). .NET's
	// Math.Clamp(value, 1, 1000) becomes the same bound, enforced here.
	CommunicationsRetentionBatchSize int
	// CommunicationsRetentionPoll is how often the retention worker runs a
	// cycle (COMMUNICATIONS_RETENTION_POLL, Communications:Retention:PollMinutes,
	// default 60 minutes). .NET's max(1, minutes) floor exists because its
	// unit is whole minutes; here the unit is a duration and the equivalent
	// rule is duration()'s own "must be positive".
	CommunicationsRetentionPoll time.Duration

	// CommunicationsAIEnabled turns the communications AI draft and
	// customer-suggestion operations on (COMMUNICATIONS_AI_ENABLED, .NET's
	// CommunicationsAiOptions.Enabled, default false). It is one of the three
	// values availability is computed from (communications inventory §17.1):
	// both operations answer 503 ai_unavailable unless this is set, the
	// provider is "openai", and an API key is configured. Read as this file's
	// strict "0"/"1" switch rather than .NET's more permissive bool binding,
	// the same shape every other flag here uses.
	CommunicationsAIEnabled bool
	// CommunicationsAIProvider names the chat provider
	// (COMMUNICATIONS_AI_PROVIDER, default "openai"). .NET registers a chat
	// client only when this equals "openai" case-insensitively
	// (DB/CommunicationsDatabaseConfiguration.cs:45-57), and so does this
	// port: any other value leaves the feature unavailable rather than
	// refusing to start, so a second provider stays additive.
	CommunicationsAIProvider string
	// CommunicationsAIModel is the chat model (COMMUNICATIONS_AI_MODEL,
	// default "gpt-4o-mini"). communications applies .NET's own 150-character
	// limit (SV/CommunicationsAiService.cs:37) where it uses the value, which
	// is also ai_interactions.model's column width.
	CommunicationsAIModel string
	// CommunicationsAIAPIKey is the provider credential
	// (COMMUNICATIONS_AI_API_KEY, .NET's CommunicationsAiOptions.ApiKey,
	// nullable). Blank leaves the feature unavailable, and per inventory
	// §17.1 no chat client, network client or provider dependency is
	// constructed at all in that state. Redacted by redact.go's mirror like
	// every other secret in this file.
	CommunicationsAIAPIKey string
}

// IsDevelopment reports whether APP_ENV=development.
func (c *Config) IsDevelopment() bool { return c.Env == Development }

// UsesHTTPS reports whether APP_URL is an https origin. Transport is the
// operator's choice per deployment — http or https, a TLS or plaintext
// database connection, TLS or plaintext SMTP — and nothing in Load judges
// it; what does follow from the choice is the Secure attribute on every
// cookie, which a browser never sends to an http origin.
func (c *Config) UsesHTTPS() bool { return strings.HasPrefix(c.AppOrigin, "https://") }

// FromOS loads configuration from the process environment.
func FromOS() (*Config, error) {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return Load(env)
}

// Load parses and validates configuration from a plain map, the shape both a
// real environment and a test fixture share. It reports every problem in one
// error.
func Load(env map[string]string) (*Config, error) {
	var p problems
	c := &Config{}

	switch env["APP_ENV"] {
	case "", string(Production):
		c.Env = Production
	case string(Development):
		c.Env = Development
	default:
		p.add("APP_ENV", `must be "production" or "development"`)
		c.Env = Production
	}

	c.CSPReportOnly = flag(&p, env, "CSP_REPORT_ONLY")

	c.DatabaseURL = databaseURL(&p, env, "DATABASE_URL", true)
	c.MigrationsDatabaseURL = databaseURL(&p, env, "MIGRATIONS_DATABASE_URL", false)
	if c.MigrationsDatabaseURL == "" {
		c.MigrationsDatabaseURL = c.DatabaseURL
	}

	c.AppOrigin, c.AppHostname = appOrigin(&p, env)
	c.BasePath = basePath(&p, env)
	c.PublicBaseURL, c.PublicOrigin, c.PublicHostname = publicBaseURL(&p, env, c.AppOrigin, c.BasePath)
	c.Port = integer(&p, env, "PORT", 8080, 1, 65535)
	c.TrustedProxyHops = integer(&p, env, "TRUSTED_PROXY_HOPS", 0, 0, 10)
	c.ShutdownTimeout = duration(&p, env, "SHUTDOWN_TIMEOUT", 30*time.Second)
	c.LogLevel = logLevel(&p, env)
	c.Branding = branding(&p, env)

	c.AppSecret = appSecret(&p, env)
	c.Sessions = sessions(&p, env)

	c.OwnersRequireMFA = ownersRequireMFA(&p, env, c)
	c.OwnersAllowInsecureNoMFA = flag(&p, env, "OWNERS_ALLOW_INSECURE_NO_MFA")
	c.MFAIssuer = env["MFA_ISSUER"]
	if c.MFAIssuer == "" {
		c.MFAIssuer = "Vantigo"
	}
	if !c.IsDevelopment() && !c.OwnersRequireMFA && !c.OwnersAllowInsecureNoMFA {
		p.add("OWNERS_REQUIRE_MFA", "must not be 0 outside development unless OWNERS_ALLOW_INSECURE_NO_MFA=1 (accepts that a compromised Owner or SystemAdmin password alone reaches every identity and tenant control-plane endpoint)")
	}

	c.InvitationLifetime = boundedDuration(&p, env, "INVITATION_LIFETIME", 168*time.Hour, 24*time.Hour, 720*time.Hour)
	c.InvitationAcceptURL = templatedURL(&p, env, "INVITATION_ACCEPT_URL", c.AppOrigin+c.BasePath+"/invitations/accept?token={token}", "{token}")
	c.PasswordResetURL = templatedURL(&p, env, "PASSWORD_RESET_URL", c.AppOrigin+c.BasePath+"/password-reset?email={email}&token={token}", "{token}", "{email}")

	c.Mail = mailConfig(&p, env, c)
	c.OIDC = oidc(&p, env)
	c.SCIM = scim(&p, env)
	c.WorkloadTokenDir = workloadTokenDir(&p, env, c.OIDC)

	c.BootstrapOwnerEmail = bootstrapOwnerEmail(&p, env)
	c.Management = management(&p, env, c)

	c.TrustedProxyCIDRs = trustedProxyCIDRs(&p, env, c)

	c.Modules = modules(&p, env)
	c.WorkersInProcess = workersInProcess(&p, env)

	c.BrregBaseURL = httpBaseURL(&p, env, "BRREG_BASE_URL", defaultBrregBaseURL)
	c.BrregTimeout = duration(&p, env, "BRREG_TIMEOUT", 15*time.Second)

	peppolLookup(&p, env, c)
	customersRegistryWorkers(&p, env, c)
	customersAnonymisation(&p, env, c)
	customersImport(&p, env, c)
	energyImport(&p, env, c)
	energySpotPrices(&p, env, c)
	energySpotPriceService(&p, env, c)
	invoicesEhf(&p, env, c)
	invoicesVipps(&p, env, c)
	c.StaffImportMaxRows = integer(&p, env, "STAFF_IMPORT_MAX_ROWS", 5000, 5000, 20000)
	staffWorkers(&p, env, c)
	calendarWorkers(&p, env, c)

	objectStorage(&p, env, c)
	communicationsAttachments(&p, env, c)
	communicationsRetention(&p, env, c)
	communicationsAI(&p, env, c)

	if len(p) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(p, "\n  "))
	}
	return c, nil
}

// NormalizeBasePath returns raw as "/prefix" (leading slash, no trailing
// slash), or "" when it is empty or "/" — serve at the domain root. Mirrors
// the .NET AppBasePathOptions.Normalized.
func NormalizeBasePath(raw string) string {
	v := strings.Trim(strings.TrimSpace(raw), "/")
	if v == "" {
		return ""
	}
	return "/" + v
}

// problems accumulates every validation failure instead of stopping at the first.
type problems []string

func (p *problems) add(field, format string, args ...any) {
	*p = append(*p, field+": "+fmt.Sprintf(format, args...))
}

// databaseURL returns the connection string after pgx's own parse of it has
// accepted it, so a malformed string fails configuration rather than the
// first connection. What transport the string asks for — TLS and which
// sslmode, a TCP host, a Unix domain socket — is the operator's choice per
// deployment; nothing here judges it. Like pgx, the parse reads PG*
// variables (PGSSLMODE, a service file) from the process environment.
func databaseURL(p *problems, env map[string]string, field string, required bool) string {
	v := env[field]
	if v == "" {
		if required {
			p.add(field, "is required")
		}
		return ""
	}
	// The parser's own error is not echoed: it can quote the connection string.
	if _, err := pgconn.ParseConfig(v); err != nil {
		p.add(field, "is not a valid PostgreSQL connection string")
		return ""
	}
	return v
}

func appOrigin(p *problems, env map[string]string) (origin, hostname string) {
	v := env["APP_URL"]
	if v == "" {
		p.add("APP_URL", "is required")
		return "", ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		p.add("APP_URL", "must be an absolute http or https URL")
		return "", ""
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		p.add("APP_URL", "must be an origin only (scheme, host and optional port); put a path prefix in APP_BASE_PATH")
		return "", ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), strings.ToLower(u.Hostname())
}

func basePath(p *problems, env map[string]string) string {
	v := NormalizeBasePath(env["APP_BASE_PATH"])
	if v == "" {
		return ""
	}
	for _, seg := range strings.Split(v[1:], "/") {
		if seg == "" || seg == "." || seg == ".." || strings.IndexFunc(seg, invalidPathRune) >= 0 {
			p.add("APP_BASE_PATH", "must be a path like /vantigo made of letters, digits, '.', '_', '~' and '-'")
			return ""
		}
	}
	return v
}

// publicBaseURL validates PUBLIC_BASE_URL (payments port design D14) and
// returns it with its origin and host name, or three empty strings when it is
// unset or invalid (reported). https only; an origin and APP_BASE_PATH and
// nothing else — no user, query or fragment, its path normalised equal to
// basePath; its host APP_URL's or another, but never APP_URL's host name on
// another port: the host filter and the public host gate compare host names
// without the port, so the two could never be told apart.
func publicBaseURL(p *problems, env map[string]string, appOrigin, basePath string) (base, origin, hostname string) {
	v := env["PUBLIC_BASE_URL"]
	if v == "" {
		return "", "", ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		p.add("PUBLIC_BASE_URL", "must be an absolute https URL")
		return "", "", ""
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		p.add("PUBLIC_BASE_URL", "must carry no user, query or fragment")
		return "", "", ""
	}
	// The host filter and the gate compare the Host header's name exactly:
	// a trailing dot or a Unicode name would match no browser's Host.
	if strings.HasSuffix(u.Hostname(), ".") {
		p.add("PUBLIC_BASE_URL", "its host name must not end in a dot")
		return "", "", ""
	}
	if strings.IndexFunc(u.Hostname(), func(r rune) bool { return r > unicode.MaxASCII }) >= 0 {
		p.add("PUBLIC_BASE_URL", "its host name must be ASCII; write an internationalised name in its punycode form (xn--…)")
		return "", "", ""
	}
	if NormalizeBasePath(u.Path) != basePath {
		p.add("PUBLIC_BASE_URL", "its path must equal APP_BASE_PATH (%q)", basePath)
		return "", "", ""
	}
	origin, hostname = strings.ToLower(u.Scheme+"://"+u.Host), strings.ToLower(u.Hostname())
	if app, err := url.Parse(appOrigin); err == nil && appOrigin != "" &&
		strings.TrimSuffix(strings.ToLower(app.Hostname()), ".") == hostname && effectivePort(app) != effectivePort(u) {
		p.add("PUBLIC_BASE_URL", "must not be APP_URL's host name on another port: the host filter compares host names without the port, so the two could not be told apart; use APP_URL's origin or another host name")
		return "", "", ""
	}
	return origin + basePath, origin, hostname
}

// effectivePort is u's port, or its scheme's default when it names none.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// defaultBrregBaseURL is data.brreg.no, Brønnøysundregisteret's public
// Enhetsregisteret API origin (customers inventory §5).
const defaultBrregBaseURL = "https://data.brreg.no"

// httpBaseURL validates an outside service's base URL (BRREG_BASE_URL,
// INVOICES_STORECOVE_BASE_URL) as appOrigin validates APP_URL: an absolute
// http or https URL. Unlike APP_URL it may carry a path, and any trailing
// slash is trimmed so the client's fixed paths never end up with a doubled
// one. Unset, or invalid (reported), it is def, trimmed the same way.
func httpBaseURL(p *problems, env map[string]string, key, def string) string {
	def = strings.TrimSuffix(def, "/")
	v := env[key]
	if v == "" {
		return def
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		p.add(key, "must be an absolute http or https URL")
		return def
	}
	return strings.TrimSuffix(v, "/")
}

// defaultPeppolSMLZone is the production Peppol network's SML zone
// (can-this-customer-receive-EHF design D5); the test network's own zone
// (participant.sml.test.tech.peppol.org) is never a default here — an
// operator who wants it sets PEPPOL_SML_ZONE explicitly.
const defaultPeppolSMLZone = "participant.sml.prod.tech.peppol.org"

// plausibleHostname is PEPPOL_SML_ZONE's own shape check (design D5's
// controller ruling): non-empty, no "://" (a caller pasted a URL, not a
// zone), none of the characters ("/", ":", any whitespace) that cannot be
// part of a DNS name — ":" specifically catches a caller who pasted a
// host:port, the shape PEPPOL_DNS_SERVER takes, not a zone — and no empty
// label (a leading or trailing ".", or two consecutive ones), which a real
// zone never has. It does not attempt to validate the zone is a real,
// resolvable one — that is what a lookup itself finds out, and failing
// there (an upstream 502) is the honest way to report it, not a startup
// refusal for a zone this process never resolves until asked to.
func plausibleHostname(v string) bool {
	if v == "" || strings.Contains(v, "://") {
		return false
	}
	if strings.ContainsAny(v, "/: \t\n\r") {
		return false
	}
	for _, label := range strings.Split(v, ".") {
		if label == "" {
			return false
		}
	}
	return true
}

// peppolLookup resolves the customers module's Peppol lookup configuration
// (PEPPOL_LOOKUP_ENABLED, PEPPOL_SML_ZONE, PEPPOL_DNS_SERVER, PEPPOL_TIMEOUT
// — can-this-customer-receive-EHF design D3, D5): the on/off switch, the SML
// zone a lookup hashes a participant identifier into, an optional resolver
// override, and the per-lookup timeout. Mirrors httpBaseURL/BrregTimeout's
// own shape: every problem reported, defaults substituted so Load can keep
// validating the rest of the environment.
func peppolLookup(p *problems, env map[string]string, c *Config) {
	c.PeppolLookupEnabled = boolean(p, env, "PEPPOL_LOOKUP_ENABLED", true)

	c.PeppolSMLZone = strings.TrimSpace(env["PEPPOL_SML_ZONE"])
	switch {
	case c.PeppolSMLZone == "":
		c.PeppolSMLZone = defaultPeppolSMLZone
	case !plausibleHostname(c.PeppolSMLZone):
		p.add("PEPPOL_SML_ZONE", "must be a plain hostname: no scheme, port, path, whitespace or empty label")
		c.PeppolSMLZone = defaultPeppolSMLZone
	}

	if v := env["PEPPOL_DNS_SERVER"]; v != "" {
		host, port, err := net.SplitHostPort(v)
		switch {
		case err != nil || host == "":
			p.add("PEPPOL_DNS_SERVER", "must be host:port")
		case !isNumericPort(port):
			p.add("PEPPOL_DNS_SERVER", "must be host:port with a numeric port")
		default:
			c.PeppolDNSServer = v
		}
	}

	c.PeppolTimeout = duration(p, env, "PEPPOL_TIMEOUT", 10*time.Second)
}

// customersRegistryWorkers loads the two customers background workers'
// settings (registry workers design D7): each worker's switch and cadence, and
// the age at which a stored Peppol lookup is asked again. Both switches default
// on, like WORKERS_IN_PROCESS and PEPPOL_LOOKUP_ENABLED and unlike flag()'s
// fail-safe-off switches: these workers are the delivery, not an extra.
//
// Page size, page budget, sweep batches and the Peppol batch are constants in
// the workers themselves, not knobs (design D7): they bound one cycle's work
// against a public register, and an operator who could raise them could make
// this installation look like an attack.
func customersRegistryWorkers(p *problems, env map[string]string, c *Config) {
	c.CustomersRegistryFeedEnabled = boolean(p, env, "CUSTOMERS_REGISTRY_FEED_ENABLED", true)
	c.CustomersRegistryFeedPoll = duration(p, env, "CUSTOMERS_REGISTRY_FEED_POLL", 15*time.Minute)
	c.CustomersPeppolRecheckEnabled = boolean(p, env, "CUSTOMERS_PEPPOL_RECHECK_ENABLED", true)
	c.CustomersPeppolRecheckPoll = duration(p, env, "CUSTOMERS_PEPPOL_RECHECK_POLL", 24*time.Hour)
	c.CustomersPeppolRecheckAge = duration(p, env, "CUSTOMERS_PEPPOL_RECHECK_AGE", 720*time.Hour)
}

// customersAnonymisation loads the anonymisation worker's settings (customers
// GDPR design D4): its switch, on by default, and its cadence. The batch — at
// most fifty customers a cycle — is a constant in the worker, not a knob: each
// customer is one transaction holding its row, and a cycle is meant to finish.
func customersAnonymisation(p *problems, env map[string]string, c *Config) {
	c.CustomersAnonymisationEnabled = boolean(p, env, "CUSTOMERS_ANONYMISATION_ENABLED", true)
	c.CustomersAnonymisationPoll = duration(p, env, "CUSTOMERS_ANONYMISATION_POLL", 24*time.Hour)
}

// customersImport loads the import worker's settings (customers import wizard
// design D6): its switch, on by default, its poll and the retention of a
// finished import. The chunk — five hundred rows between progress writes —
// is a constant in the worker, not a knob.
func customersImport(p *problems, env map[string]string, c *Config) {
	c.CustomersImportEnabled = boolean(p, env, "CUSTOMERS_IMPORT_ENABLED", true)
	c.CustomersImportPoll = duration(p, env, "CUSTOMERS_IMPORT_POLL", 5*time.Second)
	c.CustomersImportRetention = duration(p, env, "CUSTOMERS_IMPORT_RETENTION", 720*time.Hour)
}

// energyImport loads the energy import worker's settings (energy import
// design D6), the customers import's three under energy's name: its switch,
// on by default, its poll and the retention of a finished import.
func energyImport(p *problems, env map[string]string, c *Config) {
	c.EnergyImportEnabled = boolean(p, env, "ENERGY_IMPORT_ENABLED", true)
	c.EnergyImportPoll = duration(p, env, "ENERGY_IMPORT_POLL", 5*time.Second)
	c.EnergyImportRetention = duration(p, env, "ENERGY_IMPORT_RETENTION", 720*time.Hour)
}

// The spot price feed's defaults and bounds (energy consumption billing
// design D2): the public feed's address, a quarter of an hour between cycles
// and no poll under a minute — the feed publishes once a day.
const (
	DefaultEnergySpotPriceFeedURL  = "https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json"
	defaultEnergySpotPriceFeedPoll = 15 * time.Minute
	energySpotPriceFeedMinPoll     = time.Minute
)

// energySpotPriceFeedPlaceholders are what a feed template must carry.
var energySpotPriceFeedPlaceholders = []string{"{year}", "{month}", "{day}", "{area}"}

// energySpotPrices loads the spot price worker's settings: its switch, off
// by default, the feed's template and the poll.
func energySpotPrices(p *problems, env map[string]string, c *Config) {
	c.EnergySpotPriceFeedEnabled = boolean(p, env, "ENERGY_SPOT_PRICE_FEED_ENABLED", false)
	c.EnergySpotPriceFeedURL = spotPriceFeedURL(p, env, "ENERGY_SPOT_PRICE_FEED_URL")
	c.EnergySpotPriceFeedPoll = duration(p, env, "ENERGY_SPOT_PRICE_FEED_POLL", defaultEnergySpotPriceFeedPoll)
	if c.EnergySpotPriceFeedPoll < energySpotPriceFeedMinPoll {
		p.add("ENERGY_SPOT_PRICE_FEED_POLL", "must be at least 1m")
		c.EnergySpotPriceFeedPoll = defaultEnergySpotPriceFeedPoll
	}
}

// energySpotPriceServiceKeyMax bounds the service key's length; the
// service's keys are a few dozen bytes.
const energySpotPriceServiceKeyMax = 200

// energySpotPriceService loads the spot-price service's settings (spot-price
// service design S3): its URL and key, the oldest day to copy, and the rule
// that one installation has one spot price source. The problems never echo
// the key.
func energySpotPriceService(p *problems, env map[string]string, c *Config) {
	raw := strings.TrimSpace(env["ENERGY_SPOT_PRICE_SERVICE_URL"])
	key := strings.TrimSpace(env["ENERGY_SPOT_PRICE_SERVICE_KEY"])
	if raw != "" {
		u, err := url.Parse(raw)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "":
			p.add("ENERGY_SPOT_PRICE_SERVICE_URL", "must be an absolute http or https URL")
		case u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/"):
			p.add("ENERGY_SPOT_PRICE_SERVICE_URL", "must be the service's base URL, with no user, path, query or fragment")
		default:
			c.EnergySpotPriceServiceURL = strings.TrimSuffix(raw, "/")
		}
		if c.EnergySpotPriceFeedEnabled {
			p.add("ENERGY_SPOT_PRICE_SERVICE_URL", "set one spot price source: the service or ENERGY_SPOT_PRICE_FEED_ENABLED, not both")
		}
		switch {
		case key == "":
			p.add("ENERGY_SPOT_PRICE_SERVICE_KEY", "is required with ENERGY_SPOT_PRICE_SERVICE_URL")
		case len(key) > energySpotPriceServiceKeyMax || strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r == 0x7f }):
			p.add("ENERGY_SPOT_PRICE_SERVICE_KEY", "must be at most 200 bytes of printable characters with no space")
		default:
			c.EnergySpotPriceServiceKey = key
		}
	} else if key != "" {
		p.add("ENERGY_SPOT_PRICE_SERVICE_KEY", "is set without ENERGY_SPOT_PRICE_SERVICE_URL")
	}
	if v := strings.TrimSpace(env["ENERGY_SPOT_PRICE_HISTORY_FROM"]); v != "" {
		day, err := time.Parse(time.DateOnly, v)
		if err != nil {
			p.add("ENERGY_SPOT_PRICE_HISTORY_FROM", "must be a date such as 2021-12-01")
		} else {
			c.EnergySpotPriceHistoryFrom = day
		}
	}
}

// spotPriceFeedURL reads the feed template: unset is the default; set, it
// must be an absolute https URL carrying every placeholder.
func spotPriceFeedURL(p *problems, env map[string]string, field string) string {
	v := strings.TrimSpace(env[field])
	if v == "" {
		return DefaultEnergySpotPriceFeedURL
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		p.add(field, "must be an absolute https URL")
		return DefaultEnergySpotPriceFeedURL
	}
	for _, ph := range energySpotPriceFeedPlaceholders {
		if !strings.Contains(v, ph) {
			p.add(field, "must contain {year}, {month}, {day} and {area}")
			return DefaultEnergySpotPriceFeedURL
		}
	}
	return v
}

// staffWorkersMinPoll is the shortest STAFF_WORKERS_POLL accepted.
const staffWorkersMinPoll = time.Minute

// staffWorkers loads the Staff workers' settings (staff foundation design
// §8.5, staff competence design S7): the retention, the accounts and the
// competence workers' switches, each on by default, and the poll they share, an hour by default and at least a
// minute. The batch —
// at most fifty people an erase cycle and a hundred objects a ledger pass — is
// a constant in the worker, not a knob.
func staffWorkers(p *problems, env map[string]string, c *Config) {
	c.StaffRetentionEnabled = boolean(p, env, "STAFF_RETENTION_ENABLED", true)
	c.StaffAccountsEnabled = boolean(p, env, "STAFF_ACCOUNTS_ENABLED", true)
	c.StaffCompetenceEnabled = boolean(p, env, "STAFF_COMPETENCE_ENABLED", true)
	c.StaffWorkersPoll = time.Hour
	if v := env["STAFF_WORKERS_POLL"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < staffWorkersMinPoll {
			p.add("STAFF_WORKERS_POLL", "must be a duration of at least 1m, such as 1h")
			return
		}
		c.StaffWorkersPoll = d
	}
}

// calendarWorkersMinPoll is the shortest CALENDAR_WORKERS_POLL accepted.
const calendarWorkersMinPoll = time.Minute

// calendarSubscriptionsMinPoll is the shortest CALENDAR_SUBSCRIPTIONS_POLL
// accepted: the subscriptions worker's own tick, below which a poll cannot be
// kept.
const calendarSubscriptionsMinPoll = 5 * time.Minute

// calendarMailboxSyncMinPoll is the shortest CALENDAR_MAILBOX_SYNC_POLL
// accepted: the mailbox sync worker's own tick.
const calendarMailboxSyncMinPoll = time.Minute

// calendarWorkers loads the calendar workers' settings (calendar invitations
// design D6, calendar feed design D7): the cancellations worker's switch, on
// by default, and the poll, an hour by default and at least a minute; the
// subscriptions worker's switch, on by default, and how often each
// subscription is fetched, half an hour by default and at least five minutes;
// the mailbox sync worker's switch, on by default, and how often each
// person's copies are synced, a quarter of an hour by default and at least a
// minute (calendar Outlook sync design O7). The batches — at most two hundred appointments, and two hundred
// subscriptions, a cycle — are constants in the workers, not knobs. Each poll
// is read on its own, so one refused leaves the other read and both are
// reported.
func calendarWorkers(p *problems, env map[string]string, c *Config) {
	c.CalendarCancellationsEnabled = boolean(p, env, "CALENDAR_CANCELLATIONS_ENABLED", true)
	c.CalendarWorkersPoll = time.Hour
	if v := env["CALENDAR_WORKERS_POLL"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < calendarWorkersMinPoll {
			p.add("CALENDAR_WORKERS_POLL", "must be a duration of at least 1m, such as 1h")
		} else {
			c.CalendarWorkersPoll = d
		}
	}
	c.CalendarSubscriptionsEnabled = boolean(p, env, "CALENDAR_SUBSCRIPTIONS_ENABLED", true)
	c.CalendarSubscriptionsPoll = 30 * time.Minute
	if v := env["CALENDAR_SUBSCRIPTIONS_POLL"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < calendarSubscriptionsMinPoll {
			p.add("CALENDAR_SUBSCRIPTIONS_POLL", "must be a duration of at least 5m, such as 30m")
		} else {
			c.CalendarSubscriptionsPoll = d
		}
	}
	c.CalendarMailboxSyncEnabled = boolean(p, env, "CALENDAR_MAILBOX_SYNC_ENABLED", true)
	c.CalendarMailboxSyncPoll = 15 * time.Minute
	if v := env["CALENDAR_MAILBOX_SYNC_POLL"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < calendarMailboxSyncMinPoll {
			p.add("CALENDAR_MAILBOX_SYNC_POLL", "must be a duration of at least 1m, such as 15m")
		} else {
			c.CalendarMailboxSyncPoll = d
		}
	}
	c.CalendarMailboxNotificationsEnabled = boolean(p, env, "CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED", true)
}

// defaultStorecoveBaseURL is Storecove's production API (EHF and KID design
// D1, the spike's OpenAPI document); the sandbox is the same host with a
// sandbox key.
const defaultStorecoveBaseURL = "https://api.storecove.com/api/v2/"

// invoicesEhf loads the Invoices e-invoicing settings (EHF and KID design D1):
// the switch, on by default, and the Storecove base URL, validated by
// httpBaseURL as BRREG_BASE_URL is — an absolute http or https URL, any
// trailing slash trimmed so the adapter's fixed paths never double one.
func invoicesEhf(p *problems, env map[string]string, c *Config) {
	c.InvoicesEhfEnabled = boolean(p, env, "INVOICES_EHF_ENABLED", true)
	c.InvoicesStorecoveBaseURL = httpBaseURL(p, env, "INVOICES_STORECOVE_BASE_URL", defaultStorecoveBaseURL)
}

// defaultVippsBaseURL is Vipps MobilePay's production API (payments port
// design D13); the test environment is https://apitest.vipps.no, set
// explicitly by an operator who wants it.
const defaultVippsBaseURL = "https://api.vipps.no"

// invoicesVipps loads the Invoices Vipps MobilePay settings (payments port
// design D1): the switch, on by default, and the base URL, validated by
// httpBaseURL as INVOICES_STORECOVE_BASE_URL is.
func invoicesVipps(p *problems, env map[string]string, c *Config) {
	c.InvoicesVippsEnabled = boolean(p, env, "INVOICES_VIPPS_ENABLED", true)
	c.InvoicesVippsBaseURL = httpBaseURL(p, env, "INVOICES_VIPPS_BASE_URL", defaultVippsBaseURL)
}

// isNumericPort reports whether port is all ASCII digits, at least one:
// net.SplitHostPort already rejects a value with no colon, but accepts
// "host:" or "host:abc" alike, leaving the port itself unchecked.
func isNumericPort(port string) bool {
	if port == "" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// objectStorage resolves STORAGE_PROVIDER, STORAGE_FS_ROOT and
// STORAGE_FS_ALLOW_INSECURE_ROOT (design §7; docs/src/content/docs/en/admin/object-storage.md "Local
// filesystem"). STORAGE_PROVIDER is the on/off switch: unset leaves storage
// unconfigured rather than refusing to start — internal/storage.New returns
// a store whose every operation fails closed, mirroring .NET's host, which
// starts even with storage unconfigured (docs/src/content/docs/en/admin/object-storage.md).
// STORAGE_FS_ALLOW_INSECURE_ROOT follows the same shape as MAIL_DRIVER=log
// and OWNERS_ALLOW_INSECURE_NO_MFA: accepted, and meaningful, only when the
// environment truly is development; set outside development it is a
// configuration error regardless of what it would have done.
func objectStorage(p *problems, env map[string]string, c *Config) {
	switch env["STORAGE_PROVIDER"] {
	case "":
		// Unconfigured: fails closed at each operation, not at startup.
	case "fs":
		c.StorageProvider = "fs"
	default:
		p.add("STORAGE_PROVIDER", `must be "fs" if set`)
	}

	c.StorageFSRoot = env["STORAGE_FS_ROOT"]
	switch {
	case c.StorageProvider == "fs" && strings.TrimSpace(c.StorageFSRoot) == "":
		p.add("STORAGE_FS_ROOT", `is required when STORAGE_PROVIDER is "fs"`)
	case c.StorageProvider == "fs" && !filepath.IsAbs(c.StorageFSRoot):
		p.add("STORAGE_FS_ROOT", "must be an absolute path")
	case c.StorageProvider != "fs" && c.StorageFSRoot != "":
		p.add("STORAGE_FS_ROOT", `is only valid when STORAGE_PROVIDER is "fs"`)
	}

	c.StorageFSAllowInsecureRoot = flag(p, env, "STORAGE_FS_ALLOW_INSECURE_ROOT")
	if c.StorageFSAllowInsecureRoot && !c.IsDevelopment() {
		p.add("STORAGE_FS_ALLOW_INSECURE_ROOT", "must not be 1 outside development")
	}
}

// communicationsAttachments loads the two settings communications' attachment
// staging needs that .NET carried on its now-removed scanner options
// (design doc D3): the per-file size limit and the staged-upload expiry; and
// the largest inbound message the mailbox sync imports.
func communicationsAttachments(p *problems, env map[string]string, c *Config) {
	c.CommunicationsAttachmentMaxBytes = int64(integer(p, env, "COMMUNICATIONS_ATTACHMENT_MAX_BYTES", 10*1024*1024, 1, 50*1024*1024))
	c.CommunicationsUploadExpiry = duration(p, env, "COMMUNICATIONS_UPLOAD_EXPIRY", 24*time.Hour)
	c.CommunicationsInboundMaxBytes = int64(integer(p, env, "COMMUNICATIONS_INBOUND_MAX_BYTES", 25*1024*1024, 1024*1024, 150*1024*1024))
}

// communicationsRetention loads CommunicationsOptions.Retention
// (communications inventory §12.1, design doc §7): the retention window, the
// per-batch cap and the worker's poll cadence, whose .NET defaults are 365
// days, 100 messages and 60 minutes.
func communicationsRetention(p *problems, env map[string]string, c *Config) {
	c.CommunicationsRetentionDays = integer(p, env, "COMMUNICATIONS_RETENTION_DAYS", 365, 1, 36500)
	c.CommunicationsRetentionBatchSize = integer(p, env, "COMMUNICATIONS_RETENTION_BATCH_SIZE", 100, 1, 1000)
	c.CommunicationsRetentionPoll = duration(p, env, "COMMUNICATIONS_RETENTION_POLL", time.Hour)
}

// communicationsAI loads CommunicationsAiOptions (communications inventory
// §17.1, design doc §7): the on/off switch, the provider and model names,
// and the API key. Nothing here refuses to start — an unset or half-set AI
// configuration is the documented "unavailable" state the two AI operations
// answer 503 for, not a misconfiguration, exactly as .NET simply declines to
// register a chat client for it. Only COMMUNICATIONS_AI_ENABLED can be
// rejected, and only for not being the strict "0"/"1" flag reads.
func communicationsAI(p *problems, env map[string]string, c *Config) {
	c.CommunicationsAIEnabled = flag(p, env, "COMMUNICATIONS_AI_ENABLED")
	c.CommunicationsAIProvider = strings.TrimSpace(env["COMMUNICATIONS_AI_PROVIDER"])
	if c.CommunicationsAIProvider == "" {
		c.CommunicationsAIProvider = "openai"
	}
	c.CommunicationsAIModel = strings.TrimSpace(env["COMMUNICATIONS_AI_MODEL"])
	if c.CommunicationsAIModel == "" {
		c.CommunicationsAIModel = "gpt-4o-mini"
	}
	c.CommunicationsAIAPIKey = strings.TrimSpace(env["COMMUNICATIONS_AI_API_KEY"])
}

func invalidPathRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.' || r == '_' || r == '~' || r == '-':
		return false
	}
	return true
}

func integer(p *problems, env map[string]string, field string, def, min, max int) int {
	v := env[field]
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		p.add(field, "must be an integer from %d to %d", min, max)
		return def
	}
	return n
}

func duration(p *problems, env map[string]string, field string, def time.Duration) time.Duration {
	v := env[field]
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.add(field, "must be a positive duration such as 30s")
		return def
	}
	return d
}

// boolean reads a strict "0"/"1" switch; unset means def. Anything else is
// rejected rather than guessed at, and the default stands.
func boolean(p *problems, env map[string]string, field string, def bool) bool {
	switch env[field] {
	case "":
		return def
	case "0":
		return false
	case "1":
		return true
	default:
		p.add(field, `must be "0" or "1"`)
		return def
	}
}

// flag is boolean with the fail-safe default: off.
func flag(p *problems, env map[string]string, field string) bool {
	return boolean(p, env, field, false)
}

func logLevel(p *problems, env map[string]string) slog.Level {
	switch strings.ToLower(env["LOG_LEVEL"]) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		p.add("LOG_LEVEL", `must be one of "debug", "info", "warn", "error"`)
		return slog.LevelInfo
	}
}

func branding(p *problems, env map[string]string) Branding {
	b := Branding{
		Title:        strings.TrimSpace(env["APP_TITLE"]),
		SupportPhone: strings.TrimSpace(env["APP_SUPPORT_PHONE"]),
		LogoURL:      linkField(p, env, "APP_LOGO_URL"),
		SupportURL:   linkField(p, env, "APP_SUPPORT_URL"),
	}
	if v := strings.TrimSpace(env["APP_SUPPORT_EMAIL"]); v != "" {
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			p.add("APP_SUPPORT_EMAIL", "must be a plain email address such as support@example.com")
		} else {
			b.SupportEmail = v
		}
	}
	return b
}

// linkField accepts an absolute http(s) URL or a same-origin path. Anything
// else (javascript:, protocol-relative //host, or /\host, which a browser
// reads as //host) would end up in an href or src the SPA renders.
func linkField(p *problems, env map[string]string, field string) string {
	v := strings.TrimSpace(env[field])
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") && !strings.HasPrefix(v, `/\`) {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.add(field, "must be an absolute http or https URL or a path starting with /")
		return ""
	}
	return v
}

// appSecret reads the process-wide key material. Its value is never included
// in a problem message: only its length is ever reported.
func appSecret(p *problems, env map[string]string) []byte {
	v := env["APP_SECRET"]
	if len(v) < 32 {
		p.add("APP_SECRET", "must be at least 32 bytes")
		return nil
	}
	return []byte(v)
}

// sessions parses the four session bounds and rejects a privileged bound
// looser than its standard counterpart, which would defeat the point of
// having a tighter pair for Owner and SystemAdmin sessions.
func sessions(p *problems, env map[string]string) SessionConfig {
	s := SessionConfig{
		Idle:               duration(p, env, "SESSION_IDLE_TIMEOUT", 8*time.Hour),
		PrivilegedIdle:     duration(p, env, "SESSION_PRIVILEGED_IDLE_TIMEOUT", 2*time.Hour),
		Absolute:           duration(p, env, "SESSION_ABSOLUTE_LIFETIME", 24*time.Hour),
		PrivilegedAbsolute: duration(p, env, "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", 8*time.Hour),
	}
	if s.PrivilegedIdle > s.Idle {
		p.add("SESSION_PRIVILEGED_IDLE_TIMEOUT", "must not exceed SESSION_IDLE_TIMEOUT")
	}
	if s.PrivilegedAbsolute > s.Absolute {
		p.add("SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", "must not exceed SESSION_ABSOLUTE_LIFETIME")
	}
	return s
}

// ownersRequireMFA defaults to on in production and off in development: a
// compromised Owner or SystemAdmin password alone must not reach the control
// plane unless the operator has knowingly accepted that with
// OWNERS_ALLOW_INSECURE_NO_MFA (checked once both flags are parsed).
func ownersRequireMFA(p *problems, env map[string]string, c *Config) bool {
	return boolean(p, env, "OWNERS_REQUIRE_MFA", !c.IsDevelopment())
}

// boundedDuration is duration plus a [min,max] range. A syntactically valid
// duration outside the range is its own problem, reported once: duration
// already reports an unparsable or non-positive value.
func boundedDuration(p *problems, env map[string]string, field string, def, min, max time.Duration) time.Duration {
	d := duration(p, env, field, def)
	if d < min || d > max {
		p.add(field, "must be from %s to %s", min, max)
		return def
	}
	return d
}

// templatedURL falls back to def (typically derived from APP_URL) when unset,
// mirroring InvitationTokenService's same-origin default. Whichever URL is
// effective, every placeholder (such as "{token}") must appear in it
// literally, so the caller can substitute it safely.
func templatedURL(p *problems, env map[string]string, field, def string, placeholders ...string) string {
	v := env[field]
	if v == "" {
		v = def
	}
	for _, ph := range placeholders {
		if !strings.Contains(v, ph) {
			p.add(field, "must contain the %s placeholder", ph)
			return def
		}
	}
	return v
}

// mailConfig resolves the application mail driver. The "log" driver is a
// development convenience: invitation and password-reset mails carry bearer
// links, so writing them to the log anywhere else is a leak, not a feature.
func mailConfig(p *problems, env map[string]string, c *Config) MailConfig {
	var m MailConfig
	switch env["MAIL_DRIVER"] {
	case "":
		if c.IsDevelopment() {
			m.Driver = "log"
		} else {
			m.Driver = "smtp"
		}
	case "smtp":
		m.Driver = "smtp"
	case "log":
		if !c.IsDevelopment() {
			p.add("MAIL_DRIVER", `must not be "log" outside development`)
		}
		m.Driver = "log"
	default:
		p.add("MAIL_DRIVER", `must be "smtp" or "log"`)
		m.Driver = "smtp"
	}

	m.Username = env["SMTP_USERNAME"]
	m.Password = env["SMTP_PASSWORD"]
	m.Port = integer(p, env, "SMTP_PORT", 587, 1, 65535)

	if m.Driver == "smtp" {
		if m.Host = env["SMTP_HOST"]; m.Host == "" {
			p.add("SMTP_HOST", "is required")
		}
		if v := env["SMTP_FROM"]; v == "" {
			p.add("SMTP_FROM", "is required")
		} else if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			p.add("SMTP_FROM", "must be a plain email address such as noreply@example.com")
		} else {
			m.From = v
		}
	}

	switch env["SMTP_TLS"] {
	case "":
		m.TLS = "starttls"
	case "implicit", "starttls", "none":
		m.TLS = env["SMTP_TLS"]
	default:
		p.add("SMTP_TLS", `must be "implicit", "starttls" or "none"`)
		m.TLS = "starttls"
	}

	return m
}

// entraAuthorityPattern and guidPattern mirror
// WorkforceOidcOptions.TryValidateEntraAuthority and its ClientId check.
var (
	entraAuthorityPattern = regexp.MustCompile(`^https://login\.microsoftonline\.com/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/v2\.0$`)
	guidPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// oidc resolves the one workforce OpenID Connect provider. OIDC_PROVIDER is
// the on/off switch: empty disables it (a nil *OIDCConfig), set enables it
// and every other OIDC_* setting is then validated for that provider.
// Mirrors WorkforceOidcOptions.FromAuthenticationOptions.
func oidc(p *problems, env map[string]string) *OIDCConfig {
	provider := env["OIDC_PROVIDER"]
	if provider == "" {
		if leftover := leftoverOIDCSettings(env); len(leftover) > 0 {
			p.add("OIDC_PROVIDER", "is required because %s", areSet(leftover))
		}
		return nil
	}
	if provider != "entra" && provider != "google" {
		p.add("OIDC_PROVIDER", `must be "entra" or "google"`)
		return nil
	}
	o := &OIDCConfig{Provider: provider}

	o.Authority = env["OIDC_AUTHORITY"]
	switch provider {
	case "entra":
		if !entraAuthorityPattern.MatchString(o.Authority) {
			p.add("OIDC_AUTHORITY", "must be exactly https://login.microsoftonline.com/<tenant-guid>/v2.0")
		}
	case "google":
		if o.Authority != "https://accounts.google.com" {
			p.add("OIDC_AUTHORITY", "must be exactly https://accounts.google.com")
		}
	}

	o.ClientID = env["OIDC_CLIENT_ID"]
	switch provider {
	case "entra":
		if !guidPattern.MatchString(o.ClientID) {
			p.add("OIDC_CLIENT_ID", "must be a GUID for the entra provider")
		}
	case "google":
		if !strings.HasSuffix(o.ClientID, ".apps.googleusercontent.com") {
			p.add("OIDC_CLIENT_ID", "must end with .apps.googleusercontent.com for the google provider")
		}
	}

	secret := env["OIDC_CLIENT_SECRET"]
	tokenFile := env["OIDC_WORKLOAD_IDENTITY_TOKEN_FILE"]
	if tokenFile == "" {
		tokenFile = env["AZURE_FEDERATED_TOKEN_FILE"]
	}
	switch {
	case secret != "" && tokenFile != "":
		p.add("OIDC_CLIENT_SECRET", "and OIDC_WORKLOAD_IDENTITY_TOKEN_FILE are mutually exclusive")
	case secret == "" && tokenFile == "":
		p.add("OIDC_CLIENT_SECRET", "or OIDC_WORKLOAD_IDENTITY_TOKEN_FILE is required")
	case secret != "":
		o.ClientSecret = secret
	case provider != "entra":
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "is supported only for the entra provider")
	case !filepath.IsAbs(tokenFile):
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "must be an absolute path")
	case !readableFile(tokenFile):
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "must be a readable file")
	default:
		o.WorkloadTokenFile = tokenFile
	}

	o.AllowedDomains = allowedDomains(p, env["OIDC_ALLOWED_DOMAINS"])
	switch provider {
	case "entra":
		if len(o.AllowedDomains) > 0 {
			p.add("OIDC_ALLOWED_DOMAINS", "is only valid for the google provider")
		}
	case "google":
		if len(o.AllowedDomains) == 0 {
			p.add("OIDC_ALLOWED_DOMAINS", "must list at least one domain for the google provider")
		}
	}

	if o.DisplayName = env["OIDC_DISPLAY_NAME"]; o.DisplayName == "" {
		o.DisplayName = "Workforce SSO"
	}

	return o
}

// leftoverOIDCFields are the settings that mean nothing while OIDC_PROVIDER
// is empty, mirroring WorkforceOidcOptions.FromAuthenticationOptions's own
// "contains provider settings but Enabled is false" rejection.
// AZURE_FEDERATED_TOKEN_FILE is deliberately excluded: it is a platform
// variable other software (workload identity federation) may set regardless
// of whether OIDC is configured at all.
var leftoverOIDCFields = []string{
	"OIDC_AUTHORITY", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET",
	"OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "OIDC_ALLOWED_DOMAINS", "OIDC_DISPLAY_NAME",
}

// areSet is "A is set", "A and B are set" or "A, B and C are set" for
// fields.
func areSet(fields []string) string {
	if len(fields) == 1 {
		return fields[0] + " is set"
	}
	return strings.Join(fields[:len(fields)-1], ", ") + " and " + fields[len(fields)-1] + " are set"
}

// leftoverOIDCSettings names (never their values) the settings left behind
// when OIDC_PROVIDER is empty.
func leftoverOIDCSettings(env map[string]string) []string {
	var leftover []string
	for _, field := range leftoverOIDCFields {
		if env[field] != "" {
			leftover = append(leftover, field)
		}
	}
	return leftover
}

// defaultWorkloadTokenDir is where the Azure workload identity webhook
// projects a token when nothing in the environment says otherwise.
const defaultWorkloadTokenDir = "/var/run/secrets/azure/tokens"

// workloadTokenDir is OIDC_WORKLOAD_IDENTITY_TOKEN_DIR: the directory a
// workload identity token file saved on the Single sign-on card must lie
// in, an absolute path, cleaned. An Owner names the file, so the
// deployment, not the Owner, bounds what the server will stat, open and
// read at every sign-in on their word. Unset, it is the directory of the
// token file the environment names — OIDC_WORKLOAD_IDENTITY_TOKEN_FILE,
// else AZURE_FEDERATED_TOKEN_FILE, which the workload identity webhook
// sets — and, with neither, the webhook's own directory,
// defaultWorkloadTokenDir. A token file the environment names must lie in
// it, which it does unless the two are set apart on purpose.
func workloadTokenDir(p *problems, env map[string]string, o *OIDCConfig) string {
	dir := env["OIDC_WORKLOAD_IDENTITY_TOKEN_DIR"]
	switch {
	case dir != "":
		if !filepath.IsAbs(dir) {
			p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_DIR", "must be an absolute path")
			return ""
		}
		dir = filepath.Clean(dir)
	case filepath.IsAbs(env["OIDC_WORKLOAD_IDENTITY_TOKEN_FILE"]):
		dir = filepath.Dir(filepath.Clean(env["OIDC_WORKLOAD_IDENTITY_TOKEN_FILE"]))
	case filepath.IsAbs(env["AZURE_FEDERATED_TOKEN_FILE"]):
		dir = filepath.Dir(filepath.Clean(env["AZURE_FEDERATED_TOKEN_FILE"]))
	default:
		dir = defaultWorkloadTokenDir
	}
	if o != nil && o.WorkloadTokenFile != "" && !strings.HasPrefix(filepath.Clean(o.WorkloadTokenFile), InDirPrefix(dir)) {
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "must be a file in OIDC_WORKLOAD_IDENTITY_TOKEN_DIR")
	}
	return dir
}

// InDirPrefix is what a cleaned path must start with to lie inside dir, a
// cleaned absolute directory: dir with one trailing slash, so neither dir
// itself nor a sibling whose name merely begins with dir's passes. The
// test is lexical; a symbolic link inside dir may still lead out of it.
func InDirPrefix(dir string) string {
	return strings.TrimSuffix(dir, "/") + "/"
}

// readableFile reports whether path names a regular file this process can
// open for reading, mirroring WorkforceOidcOptions's own existence and
// readability check on the workload identity token file.
func readableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// dnsNamePattern is .NET's bare-DNS-name pattern for an allowed domain
// (WorkforceOidcOptions.NormalizeDomains) less its (?=.{1,253}$)
// lookahead, which RE2 lacks and allowedDomains checks as a length.
var dnsNamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// allowedDomains parses a comma list of bare DNS names as .NET's
// WorkforceOidcOptions.NormalizeDomains did
// (packages/configuration/Vantigo.Configuration/WorkforceOidcOptions.cs:183-198):
// each trimmed, its trailing dots stripped and lower-cased, then required
// to be 1 to 253 characters matching the bare-DNS-name pattern, so a URL,
// an address, a port, whitespace or a single label is refused; the result
// is distinct and in ordinal order. A blank entry between commas is
// skipped; one that is only dots is not blank and is refused. A refusal is
// reported once and names the variable, never a value.
func allowedDomains(p *problems, v string) []string {
	if v == "" {
		return nil
	}
	seen := map[string]bool{}
	invalid := false
	for _, raw := range strings.Split(v, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		d := strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "."))
		if len(d) == 0 || len(d) > 253 || !dnsNamePattern.MatchString(d) {
			invalid = true
			continue
		}
		seen[d] = true
	}
	if invalid {
		p.add("OIDC_ALLOWED_DOMAINS", "must contain bare DNS names such as example.com")
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// scim resolves the static SCIM bearer credential. SCIM_TOKEN is the on/off
// switch: empty disables it (a nil *SCIMConfig). Mirrors
// StaticScimOptions.FromAuthenticationOptions, minus the file-based secret
// sources (not needed by this deployment shape).
func scim(p *problems, env map[string]string) *SCIMConfig {
	token := env["SCIM_TOKEN"]
	if token == "" {
		return nil
	}
	if containsWhitespace(token) {
		p.add("SCIM_TOKEN", "must not contain whitespace")
		return nil
	}
	s := &SCIMConfig{Token: token}

	previous := env["SCIM_PREVIOUS_TOKEN"]
	expiresRaw := env["SCIM_PREVIOUS_TOKEN_EXPIRES_AT"]
	if previous == "" {
		if expiresRaw != "" {
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "requires SCIM_PREVIOUS_TOKEN")
		}
		return s
	}

	switch {
	case containsWhitespace(previous):
		p.add("SCIM_PREVIOUS_TOKEN", "must not contain whitespace")
	case previous == token:
		p.add("SCIM_PREVIOUS_TOKEN", "must differ from SCIM_TOKEN")
	default:
		s.PreviousToken = previous
	}

	switch expiresRaw {
	case "":
		p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "is required when SCIM_PREVIOUS_TOKEN is set")
	default:
		t, err := time.Parse(time.RFC3339, expiresRaw)
		switch {
		case err != nil:
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be an RFC 3339 timestamp")
		case !t.After(time.Now()):
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be in the future")
		case t.After(time.Now().Add(24 * time.Hour)):
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be at most 24h from now")
		default:
			s.PreviousTokenExpiresAt = t
		}
	}

	return s
}

func containsWhitespace(v string) bool {
	return strings.IndexFunc(v, unicode.IsSpace) >= 0
}

// bootstrapOwnerEmail is optional; the display-name form is refused so the
// value is exactly what the invitation is addressed to.
func bootstrapOwnerEmail(p *problems, env map[string]string) string {
	v := strings.TrimSpace(env["BOOTSTRAP_OWNER_EMAIL"])
	if v == "" {
		return ""
	}
	if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
		p.add("BOOTSTRAP_OWNER_EMAIL", "must be a plain email address such as owner@example.com")
		return ""
	}
	return v
}

// management resolves the management listener. The two variables are one
// switch: both set enables it, neither disables it, one alone is a mistake.
func management(p *problems, env map[string]string, c *Config) *ManagementConfig {
	port, token := env["MANAGEMENT_PORT"], env["MANAGEMENT_TOKEN"]
	switch {
	case port == "" && token == "":
		return nil
	case token == "":
		p.add("MANAGEMENT_TOKEN", "is required when MANAGEMENT_PORT is set")
		return nil
	case port == "":
		p.add("MANAGEMENT_PORT", "is required when MANAGEMENT_TOKEN is set")
		return nil
	}

	m := &ManagementConfig{Port: integer(p, env, "MANAGEMENT_PORT", 0, 1, 65535)}
	if m.Port != 0 && m.Port == c.Port {
		p.add("MANAGEMENT_PORT", "must differ from PORT")
	}
	switch {
	case containsWhitespace(token):
		p.add("MANAGEMENT_TOKEN", "must not contain whitespace")
	case len(token) < 32:
		p.add("MANAGEMENT_TOKEN", "must be at least 32 characters")
	default:
		m.Token = token
	}
	return m
}

// trustedProxyCIDRs parses TRUSTED_PROXY_CIDRS, the peers whose
// X-Forwarded-* headers are honoured. Outside development, hops without a
// list is a problem: the forwarded headers are to be honoured only from a
// peer inside the list, and with none every peer could choose the client
// address the rate limits key on. Development keeps the hops-only
// behaviour, and server.New warns about it there.
func trustedProxyCIDRs(p *problems, env map[string]string, c *Config) []netip.Prefix {
	before := len(*p)
	out := prefixes(p, env, "TRUSTED_PROXY_CIDRS")
	if len(*p) == before && len(out) == 0 && c.TrustedProxyHops > 0 && !c.IsDevelopment() {
		p.add("TRUSTED_PROXY_HOPS", "requires TRUSTED_PROXY_CIDRS outside development: forwarded headers are honoured only from a peer inside that list")
	}
	return out
}

// knownModules are the business modules MODULES may enable: openapi.Modules
// (the contract names, the one place that list is written) less identity,
// which is always mounted and never appears in MODULES.
var knownModules = func() []string {
	out := make([]string, 0, len(openapi.Modules)-1)
	for _, m := range openapi.Modules {
		if m != "identity" {
			out = append(out, m)
		}
	}
	return out
}()

// defaultModules are enabled when MODULES is unset: every business module
// this binary can mount, which is exactly knownModules. A deployment that
// does not want one of them names the others in MODULES explicitly.
var defaultModules = knownModules

// modules parses MODULES, a comma list of business module names this
// deployment enables. Entries are trimmed and lower-cased; empty entries
// (from "a,,b" or surrounding commas) are ignored. An entry outside
// knownModules is a problem naming both the offending value and the known
// set. energy depends on customers, and so do communications and projects:
// enabling any of them without customers is its own problem, naming both.
// time depends on projects the same way (it reads contracts.ProjectDirectory,
// which only projects provides), and invoices on customers (its buyer comes
// from contracts.CustomerDirectory). entra depends on staff: it is
// contracts.IdentityProvisioner's provider, and Staff's accounts worker is
// the only caller of the slot, so entra without staff would hold a tenant's
// credential that nothing ever uses (Entra design D11). planning depends on
// projects and on staff (planning design S25): it books Staff's people on
// Projects' projects, reading both through their contracts
// (contracts.ProjectDirectory and contracts.Workforce), and Customers comes
// with Projects. Identity is always mounted and is never listed here.
func modules(p *problems, env map[string]string) []string {
	v := env["MODULES"]
	if v == "" {
		return append([]string(nil), defaultModules...)
	}

	var out []string
	for _, raw := range strings.Split(v, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !slices.Contains(knownModules, name) {
			p.add("MODULES", "%q is not a known module; must be one of %s", name, strings.Join(knownModules, ", "))
			continue
		}
		out = append(out, name)
	}

	if slices.Contains(out, "energy") && !slices.Contains(out, "customers") {
		p.add("MODULES", "energy requires customers")
	}
	if slices.Contains(out, "communications") && !slices.Contains(out, "customers") {
		p.add("MODULES", "communications requires customers")
	}
	if slices.Contains(out, "projects") && !slices.Contains(out, "customers") {
		p.add("MODULES", "projects requires customers")
	}
	if slices.Contains(out, "time") && !slices.Contains(out, "projects") {
		p.add("MODULES", "time requires projects")
	}
	if slices.Contains(out, "invoices") && !slices.Contains(out, "customers") {
		p.add("MODULES", "invoices requires customers")
	}
	if slices.Contains(out, "entra") && !slices.Contains(out, "staff") {
		p.add("MODULES", "entra requires staff")
	}
	if slices.Contains(out, "planning") && !slices.Contains(out, "projects") {
		p.add("MODULES", "planning requires projects")
	}
	if slices.Contains(out, "planning") && !slices.Contains(out, "staff") {
		p.add("MODULES", "planning requires staff")
	}

	return out
}

// workersInProcess parses WORKERS_IN_PROCESS, a strict "0"/"1" switch that
// defaults to 1 (on): single-container deployments run api and worker
// together unless an operator explicitly splits worker out into its own
// replicas (design §3.2), the opposite default from flag's off-by-default
// switches.
func workersInProcess(p *problems, env map[string]string) bool {
	return boolean(p, env, "WORKERS_IN_PROCESS", true)
}

// prefixes parses a comma list of CIDR prefixes. Empty means none.
func prefixes(p *problems, env map[string]string, field string) []netip.Prefix {
	v := env[field]
	if v == "" {
		return nil
	}
	var out []netip.Prefix
	for _, raw := range strings.Split(v, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(raw)
		if err != nil {
			p.add(field, "must be a comma list of CIDR prefixes")
			return nil
		}
		out = append(out, pfx)
	}
	return out
}

package communications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file is the Channels area (EP/ChannelEndpoints.cs, communications
// inventory §1.1, §2's CreateChannel/UpdateChannel/VerifyChannel bullets,
// §15.6): getCommunicationsChannels, postCommunicationsChannels,
// getCommunicationsChannelsById, putCommunicationsChannelsById and
// postCommunicationsChannelsByIdVerify, and delivery C's
// postCommunicationsChannelsByIdProvider. All require channels-manage only
// (communications.yaml's x-vantigo-access on each operation under /channels)
// — module.Router already enforces it, so no handler here re-checks it.
//
// PROVIDERS. A channel is SMTP — its settings and sealed password on its
// channel_credentials row — or Microsoft 365 (provider "entra", Outlook
// connection design M12): an Exchange Online shared mailbox reached through
// the contracts.ChannelMailbox slot, its object id in channels.settings, no
// credential row. Mailgun, which the port never created (task 4's
// Mailgun-branch decision), left the contract with delivery C; any provider
// but these two is refused, and verifyChannel still refuses a non-SMTP one
// defensively. A Microsoft 365 channel is created by its address, or an SMTP
// channel is switched to one (POST /channels/{id}/provider, keeping its id,
// conversations and default flag); it goes back by a PUT with provider smtp
// and a full SMTP block. Every call of the slot is made before the
// transaction opens or after it commits (contractscalls.go, rule 15).

// channelCredentialPurpose is this port's internal/secrets purpose string
// for a channel's SMTP password — the domain-separation label
// PORT:379/inventory §15.6 says to carry over from .NET's
// "Communications.MailboxProvider.v1" (MailboxCredentialProtector). A value
// sealed under this purpose can never be opened under any other.
const channelCredentialPurpose = "communications/channel-smtp-credential"

// verifyTimeout bounds VerifyChannel's SMTP connection attempt (inventory
// §1.1's "10s timeout"; §15.2: "EP/ChannelEndpoints.cs:49 wraps
// verification in a 10-second CTS").
const verifyTimeout = 10 * time.Second

// errUnsupportedChannelProvider is verifyChannel's defensive refusal for a
// channel whose provider is not "smtp" — unreachable through this API's own
// create/update validation (see the Mailgun-branch note above), but handled
// rather than left to panic.
var errUnsupportedChannelProvider = errors.New("communications: channel verification is only supported for smtp channels")

// errChannelMissingCredentials is verifyChannel's separate refusal for an
// smtp channel that has no credential row at all — every channel this API
// creates gets one atomically, so this is reachable only the same way
// TestUpdateChannel_MissingCredentialWhenNoneExists reaches its own hazard
// (a credential row deleted directly). Kept distinct from
// errUnsupportedChannelProvider (fix round 1, item 4): before this split,
// a caller with a perfectly valid smtp channel but no stored credentials
// was told its provider "is only supported for smtp channels" — a
// misdirecting message for a channel that already is smtp. Both still map
// to the same 422 verification_failed on the wire (inventory §2 gives this
// hazard no documented code of its own); the split is about which failure
// the handler is actually reporting, not a new response shape.
var errChannelMissingCredentials = errors.New("communications: channel has no stored credentials to verify")

// smtpProviderSettings is SmtpProviderSettings, camelCase-serialized into
// channel_credentials.settings_json (inventory §15.6: "SettingsJson =
// camelCase-serialized SmtpProviderSettings(host, port, useSsl, username)").
// The password never appears here — it lives only in secret_ciphertext,
// sealed under channelCredentialPurpose.
type smtpProviderSettings struct {
	Host     string  `json:"host"`
	Port     int32   `json:"port"`
	UseSsl   bool    `json:"useSsl"`
	Username *string `json:"username,omitempty"`
}

// channelRow is the channel-table fields every one of ListChannels,
// GetChannelByID, InsertChannel and UpdateChannel returns, in whatever
// sqlc-generated shape that particular query happens to produce — one seam
// so channelResponseOf only has to know one shape.
type channelRow struct {
	ID          uuid.UUID
	Type        string
	Address     string
	DisplayName *string
	Provider    string
	IsDefault   bool
	IsActive    bool
	CreatedAt   time.Time
	Settings    []byte
}

func channelRowFromChannel(c store.CommunicationsChannel) channelRow {
	return channelRow{
		ID: c.ID, Type: c.Type, Address: c.Address, DisplayName: c.DisplayName,
		Provider: c.Provider, IsDefault: c.IsDefault, IsActive: c.IsActive, CreatedAt: c.CreatedAt,
		Settings: c.Settings,
	}
}

func channelRowFromGet(c store.GetChannelByIDRow) channelRow {
	return channelRow{
		ID: c.ID, Type: c.Type, Address: c.Address, DisplayName: c.DisplayName,
		Provider: c.Provider, IsDefault: c.IsDefault, IsActive: c.IsActive, CreatedAt: c.CreatedAt,
		Settings: c.Settings,
	}
}

func channelRowFromList(c store.ListChannelsRow) channelRow {
	return channelRow{
		ID: c.ID, Type: c.Type, Address: c.Address, DisplayName: c.DisplayName,
		Provider: c.Provider, IsDefault: c.IsDefault, IsActive: c.IsActive, CreatedAt: c.CreatedAt,
		Settings: c.Settings,
	}
}

// mailboxSettings is channels.settings for a Microsoft 365 channel (M12):
// the object id of the mailbox ChannelMailbox.Check resolved.
type mailboxSettings struct {
	Mailbox string `json:"mailbox"`
}

// channelMailboxOf is the mailbox a channel's settings name, "" when none
// (an SMTP channel's settings are '{}').
func channelMailboxOf(settings []byte) (string, error) {
	if len(settings) == 0 {
		return "", nil
	}
	var m mailboxSettings
	if err := json.Unmarshal(settings, &m); err != nil {
		return "", fmt.Errorf("communications: decode channel settings: %w", err)
	}
	return m.Mailbox, nil
}

// channelResponseOf is ToChannelResponse (EP/ChannelEndpoints.cs:51,
// inventory §15.6 item 13): the secret is never exposed, structurally —
// settingsJSON (nil when the channel has no credential row at all, as a
// Microsoft 365 channel never has) decodes only host/port/useSsl/username.
// A Microsoft 365 channel answers its mailbox instead, settings null.
func channelResponseOf(row channelRow, settingsJSON *string) (gen.ChannelResponse, error) {
	resp := gen.ChannelResponse{
		Id: row.ID, Type: row.Type, Address: row.Address, DisplayName: row.DisplayName,
		Provider: row.Provider, IsDefault: row.IsDefault, IsActive: row.IsActive, CreatedAt: row.CreatedAt,
	}
	if row.Provider == providerEntra {
		mailbox, err := channelMailboxOf(row.Settings)
		if err != nil {
			return gen.ChannelResponse{}, err
		}
		if mailbox != "" {
			resp.Mailbox = &struct {
				Id string `json:"id"`
			}{Id: mailbox}
		}
	}
	if settingsJSON == nil {
		return resp, nil
	}
	resp.HasCredentials = true
	var settings smtpProviderSettings
	if err := json.Unmarshal([]byte(*settingsJSON), &settings); err != nil {
		return gen.ChannelResponse{}, fmt.Errorf("communications: decode channel settings: %w", err)
	}
	resp.Settings = &struct {
		Host     *string `json:"host"`
		Port     *int32  `json:"port"`
		UseSsl   *bool   `json:"useSsl"`
		Username *string `json:"username"`
	}{
		Host:     apicommon.Ptr(settings.Host),
		Port:     apicommon.Ptr(settings.Port),
		UseSsl:   apicommon.Ptr(settings.UseSsl),
		Username: settings.Username,
	}
	return resp, nil
}

// resolveSmtpCredential validates c's host and port — "SMTP credentials
// require a host and valid port." (inventory §3.1), port 1-65535 — and, when
// valid, returns the settings to persist and the request's password
// pointer (nil when the caller omitted it; what "omitted" means is the
// caller's decision: required on create, "reuse the existing one" on
// update). ok is false, and the other two results zero, for a nil c, a
// blank host, a nil port, or a port outside 1-65535.
func resolveSmtpCredential(c *gen.SmtpChannelCredentialRequest) (settings smtpProviderSettings, password *string, ok bool) {
	if c == nil || c.Host == nil || strings.TrimSpace(*c.Host) == "" || c.Port == nil || !validSMTPPort(*c.Port) {
		return smtpProviderSettings{}, nil, false
	}
	var username *string
	if c.Username != nil && strings.TrimSpace(*c.Username) != "" {
		u := strings.TrimSpace(*c.Username)
		username = &u
	}
	return smtpProviderSettings{
		Host: strings.TrimSpace(*c.Host), Port: *c.Port,
		UseSsl: c.UseSsl != nil && *c.UseSsl, Username: username,
	}, c.Password, true
}

// createChannelInput is what validateCreateChannel resolved once every
// field passed: everything InsertChannel/InsertChannelCredential need.
type createChannelInput struct {
	channelType string
	address     string
	displayName *string
	isDefault   bool
	provider    string // providerSMTP or providerEntra
	settings    smtpProviderSettings
	password    string
}

// validateCreateChannel is ValidateChannel (EP/Dtos/CommunicationValidation.cs,
// inventory §3.1's field table), collecting every field's error rather than
// stopping at the first — the same "report every failure together" style
// this codebase's other validators use (e.g. energy's ReplaceMeter,
// customers' validateLegalIdentity). provider is "smtp" (the default),
// needing a valid smtp block, or "entra", which takes none (see this file's
// header note): an smtp block on entra, or any other provider, fails with
// inventory §3.1's message for the SMTP-vs-selected-provider mismatch.
func validateCreateChannel(body gen.CreateChannelRequest) (createChannelInput, map[string][]string) {
	errs := map[string][]string{}
	var in createChannelInput

	channelType := ""
	if body.Type != nil {
		channelType = *body.Type
	}
	if channelType != "email" {
		errs["type"] = []string{"Only the email channel is currently supported."}
	}
	in.channelType = channelType

	address := ""
	if body.Address != nil {
		address = *body.Address
	}
	if !validChannelAddress(address) {
		errs["address"] = []string{"A valid channel email address is required."}
	}
	in.address = address

	if body.DisplayName != nil && *body.DisplayName != "" {
		if !validDisplayName(*body.DisplayName) {
			errs["displayName"] = []string{"DisplayName is invalid."}
		} else {
			in.displayName = body.DisplayName
		}
	}

	in.isDefault = body.IsDefault != nil && *body.IsDefault

	provider := providerSMTP
	if body.Provider != nil && strings.TrimSpace(*body.Provider) != "" {
		provider = strings.ToLower(strings.TrimSpace(*body.Provider))
	}
	in.provider = provider
	switch {
	case provider == providerEntra && body.Smtp == nil:
	case provider != providerSMTP:
		errs["smtp"] = []string{"SMTP credentials require the smtp provider."}
	default:
		settings, password, ok := resolveSmtpCredential(body.Smtp)
		if !ok {
			errs["smtp"] = []string{"SMTP credentials require a host and valid port."}
		} else {
			in.settings = settings
			if password != nil {
				in.password = *password
			}
		}
	}

	if len(errs) > 0 {
		return createChannelInput{}, errs
	}
	return in, nil
}

func encodeCiphertext(sealed []byte) string { return base64.StdEncoding.EncodeToString(sealed) }

func decodeCiphertext(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// GetCommunicationsChannels List configured channels
// (GET /api/v1/communications/channels)
//
// GetChannelsEndpoint: a bare array, no pagination, ordered CreatedAt
// ascending (inventory §1.1's `:18`/`:25` note, §19.2 item 19).
func (s *server) GetCommunicationsChannels(ctx context.Context, _ gen.GetCommunicationsChannelsRequestObject) (gen.GetCommunicationsChannelsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("communications: list channels: %w", err)
	}
	data := make([]gen.ChannelResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := channelResponseOf(channelRowFromList(row), row.CredentialSettingsJson)
		if err != nil {
			return nil, err
		}
		data = append(data, resp)
	}
	return gen.GetCommunicationsChannels200JSONResponse(data), nil
}

// PostCommunicationsChannels Create a channel
// (POST /api/v1/communications/channels)
//
// CreateChannel (inventory §2): (1) ValidateChannel -> 400; (2) no
// existence check; (3) insert, a unique violation on (type, address) -> 409
// channel_exists. IsDefault is computed before insert as
// `request.IsDefault == true || !AnyChannelExists` — the first channel ever
// created is forced default, and any explicit default demotes every other
// row in the same call.
func (s *server) PostCommunicationsChannels(ctx context.Context, req gen.PostCommunicationsChannelsRequestObject) (gen.PostCommunicationsChannelsResponseObject, error) {
	body := gen.CreateChannelRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	in, errs := validateCreateChannel(body)
	if len(errs) > 0 {
		return gen.PostCommunicationsChannels400JSONResponse(validationErrorBody(errs)), nil
	}

	// A Microsoft 365 channel: the M12 table, before the transaction opens —
	// the stored address is Check's primary one, and its settings the
	// mailbox; no credential row.
	channelSettings := []byte("{}")
	var settingsJSON, sealed []byte
	if in.provider == providerEntra {
		info, refusal, err := s.resolveSharedMailbox(ctx, in.address)
		if err != nil {
			return nil, err
		}
		if refusal != nil {
			return createChannelRefusal(refusal), nil
		}
		in.address = info.Address
		if channelSettings, err = json.Marshal(mailboxSettings{Mailbox: info.Mailbox}); err != nil {
			return nil, fmt.Errorf("communications: encode channel settings: %w", err)
		}
	} else {
		var err error
		if settingsJSON, err = json.Marshal(in.settings); err != nil {
			return nil, fmt.Errorf("communications: encode channel settings: %w", err)
		}
		if sealed, err = s.deps.Secrets.Seal(channelCredentialPurpose, []byte(in.password)); err != nil {
			return nil, fmt.Errorf("communications: seal channel credential: %w", err)
		}
	}
	now := s.deps.Clock()

	var resp gen.ChannelResponse
	txErr := s.withTx(ctx, func(ctx context.Context, q *store.Queries) error {
		// First statement of the transaction, before AnyChannelExists: the
		// "first channel ever created is forced default" decision races too,
		// so the read it is based on must be inside the lock, not before it.
		if err := lockDefaultChannelSlot(ctx, q); err != nil {
			return err
		}
		anyExists, err := q.AnyChannelExists(ctx)
		if err != nil {
			return err
		}
		isDefault := in.isDefault || !anyExists
		if isDefault {
			if err := q.ClearDefaultChannels(ctx); err != nil {
				return err
			}
		}
		channel, err := q.InsertChannel(ctx, store.InsertChannelParams{
			ID: uuid.New(), Type: in.channelType, Address: in.address, DisplayName: in.displayName,
			Provider: in.provider, IsDefault: isDefault, IsActive: true, CreatedAt: now,
			Settings: channelSettings,
		})
		if err != nil {
			return err
		}
		if in.provider == providerEntra {
			resp, err = channelResponseOf(channelRowFromChannel(channel), nil)
			return err
		}
		if err := q.InsertChannelCredential(ctx, store.InsertChannelCredentialParams{
			ID: uuid.New(), ChannelID: channel.ID, SettingsJson: string(settingsJSON),
			SecretCiphertext: encodeCiphertext(sealed), CreatedAt: now,
		}); err != nil {
			return err
		}
		settingsStr := string(settingsJSON)
		resp, err = channelResponseOf(channelRowFromChannel(channel), &settingsStr)
		return err
	})
	if txErr != nil {
		if db.IsUniqueViolation(txErr, "ux_channels_type_address") {
			return gen.PostCommunicationsChannels409JSONResponse(flatErrorBody(
				"channel_exists", "A channel with this address already exists.")), nil
		}
		if db.IsUniqueViolation(txErr, "ux_channels_entra_mailbox") {
			return gen.PostCommunicationsChannels409JSONResponse(flatErrorBody(
				"channel_exists", "A channel for this mailbox already exists.")), nil
		}
		// The (type, is_default) partial unique index (migration
		// 00006_communications_baseline.sql:48) is the *other* thing this
		// insert could collide on: two concurrent creates that both read
		// AnyChannelExists()=false (or both explicitly requested
		// isDefault:true) racing to become the one default row. Left
		// unhandled that escapes as a raw 23505 to httpx.WriteError's
		// host-wide fallback — a bare RFC 7807 409, the wrong vocabulary
		// for every non-stats endpoint in this module (design doc §3).
		//
		// **This is now a DEFENSIVE path, not a reachable one.**
		// lockDefaultChannelSlot at the top of this transaction serialises
		// every channel writer installation-wide, so the AnyChannelExists()
		// read and the demote above both run after any concurrent create or
		// update has committed — concurrent creates behave exactly as
		// sequential ones do and no longer collide (design §6 item 15;
		// TestCreateChannel_ConcurrentCreatesSerialiseIntoExactlyOneDefault).
		// The guard stays because the index is the real invariant and a
		// future writer that forgets the lock must not resurface the bare
		// 409, but no test can drive it through this path any more.
		if db.IsUniqueViolation(txErr, "ux_channels_type_is_default") {
			return gen.PostCommunicationsChannels409JSONResponse(flatErrorBody(
				"default_channel_conflict", "Another request just set the default channel. Retry the request.")), nil
		}
		return nil, fmt.Errorf("communications: create channel: %w", txErr)
	}
	return gen.PostCommunicationsChannels201JSONResponse(resp), nil
}

// createChannelRefusal is the create's answer to a mailbox refusal, by its
// status.
func createChannelRefusal(r *mailboxRefusal) gen.PostCommunicationsChannelsResponseObject {
	body := flatErrorBody(r.code, r.message)
	switch r.status {
	case http.StatusConflict:
		return gen.PostCommunicationsChannels409JSONResponse(body)
	case http.StatusServiceUnavailable:
		return gen.PostCommunicationsChannels503JSONResponse(body)
	default:
		return gen.PostCommunicationsChannels422JSONResponse(body)
	}
}

// GetCommunicationsChannelsById Get a channel
// (GET /api/v1/communications/channels/{id})
func (s *server) GetCommunicationsChannelsById(ctx context.Context, req gen.GetCommunicationsChannelsByIdRequestObject) (gen.GetCommunicationsChannelsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetChannelByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCommunicationsChannelsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get channel: %w", err)
	}
	resp, err := channelResponseOf(channelRowFromGet(row), row.CredentialSettingsJson)
	if err != nil {
		return nil, err
	}
	return gen.GetCommunicationsChannelsById200JSONResponse(resp), nil
}

// PutCommunicationsChannelsById Update a channel
// (PUT /api/v1/communications/channels/{id})
//
// UpdateChannel — split validation across the 404 (inventory §2, the
// facts-correction in this task's brief): (1) field validation (displayName
// only) -> 400, before the lookup; (2) channel lookup -> 404 bare; (3)
// field assignment; (4) credential validation -> 400, keyed by "smtp" or
// "credentials", after the lookup. So an invalid displayName against a
// missing id is 400, while an invalid or absent credential against a
// missing id is 404 — the single most likely thing to get backwards in
// this task.
func (s *server) PutCommunicationsChannelsById(ctx context.Context, req gen.PutCommunicationsChannelsByIdRequestObject) (gen.PutCommunicationsChannelsByIdResponseObject, error) {
	if req.Body == nil {
		return gen.PutCommunicationsChannelsById400JSONResponse(validationErrorBody(map[string][]string{
			"request": {"A request body is required."},
		})), nil
	}
	body := *req.Body

	// Step 1: field validation, entirely before the lookup.
	fieldErrs := map[string][]string{}
	clearDisplayName := false
	var newDisplayName *string
	if body.DisplayName != nil {
		switch {
		case *body.DisplayName == "":
			clearDisplayName = true // inventory §19.2 item 11: "" clears to null.
		case !validDisplayName(*body.DisplayName):
			fieldErrs["displayName"] = []string{"DisplayName is invalid."}
		default:
			newDisplayName = body.DisplayName
		}
	}
	if len(fieldErrs) > 0 {
		return gen.PutCommunicationsChannelsById400JSONResponse(validationErrorBody(fieldErrs)), nil
	}

	// Step 2: the lookup.
	q := store.New(s.deps.Pool)
	existing, err := q.GetChannelByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCommunicationsChannelsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get channel: %w", err)
	}

	// Step 4 (there is no step 3 worth naming separately — field assignment
	// happens inside the transaction below): credential validation, only
	// now that the channel is known to exist.
	//
	// The provider defaults to the channel's own (a PUT carrying only
	// isActive on a Microsoft 365 channel needs no credential). provider
	// entra on an SMTP channel is refused: the switch is POST
	// /channels/{id}/provider. provider smtp on a Microsoft 365 channel is
	// the way back, and needs a full smtp block, the password included —
	// there is no stored one to reuse.
	credErrs := map[string][]string{}
	provider := existing.Provider
	if body.Provider != nil && strings.TrimSpace(*body.Provider) != "" {
		provider = strings.ToLower(strings.TrimSpace(*body.Provider))
	}
	backToSMTP := existing.Provider == providerEntra && provider == providerSMTP

	var rewriteCredential bool
	var newSettingsJSON string
	// suppliedPassword is the password the REQUEST carried, or nil when it
	// omitted one and the existing password is to be reused. It is a
	// decision, not a byte: the merge that turns it into a ciphertext runs
	// inside the transaction (see the transaction body).
	var suppliedPassword *string
	switch {
	case provider == providerEntra && existing.Provider != providerEntra:
		credErrs["provider"] = []string{"A channel is switched to Microsoft 365 through its provider switch, not an update."}
	case provider == providerEntra && body.Smtp == nil:
		// A Microsoft 365 channel's own fields only: no credential.
	case provider != providerSMTP:
		credErrs["smtp"] = []string{"SMTP credentials require the smtp provider."}
	case backToSMTP && body.Smtp == nil:
		credErrs["smtp"] = []string{"SMTP credentials require a host and valid port."}
	case body.Smtp != nil:
		settings, password, ok := resolveSmtpCredential(body.Smtp)
		if !ok {
			credErrs["smtp"] = []string{"SMTP credentials require a host and valid port."}
			break
		}
		if backToSMTP && password == nil {
			credErrs["smtp"] = []string{"A password is required to switch a channel to SMTP."}
			break
		}
		b, merr := json.Marshal(settings)
		if merr != nil {
			return nil, fmt.Errorf("communications: encode channel settings: %w", merr)
		}
		// DECISIONS HERE, BYTES IN THE TRANSACTION. Everything this block
		// still does is derived from the REQUEST alone — whether a
		// credential is being rewritten, what its settings are, and whether
		// the caller supplied a password or wants the stored one reused — so
		// the validation order, and with it the position of the credential
		// 400 that inventory §19.2 pins, is exactly where it was.
		//
		// What used to happen here and no longer does is the *merge*: this
		// code read the stored credential through the pool and sealed the
		// result into newCiphertext, all before BeginTx. Two concurrent PUTs
		// each carrying an smtp block — one reusing the password, one
		// rotating it — would then have the reusing one write its
		// pre-transaction snapshot of the old password back over the rotated
		// one. Silently: no constraint covers this, so there is no 23505, no
		// error and no log, and the credential simply reverts. The merge now
		// runs under the lock against a row re-read inside the transaction.
		suppliedPassword = password
		newSettingsJSON, rewriteCredential = string(b), true
	default:
		if existing.CredentialSettingsJson == nil {
			credErrs["smtp"] = []string{"Credentials for the selected provider are required."}
		}
	}
	if len(credErrs) > 0 {
		return gen.PutCommunicationsChannelsById400JSONResponse(validationErrorBody(credErrs)), nil
	}

	// isActive, isDefault, the display name and the credential settings are
	// all resolved INSIDE the transaction now, against a row re-read under
	// the lock — see the transaction body. They used to be computed here,
	// from `existing`, which is read through the pool before the transaction
	// begins; that made every one of them a stale-snapshot write.
	//
	// inventory §19.2 item 10: IsDefault is write-once-true — an explicit
	// false is silently ignored, there is no way to clear the default flag
	// through this API.
	wantDefault := body.IsDefault != nil && *body.IsDefault
	now := s.deps.Clock()

	var resp gen.ChannelResponse
	// forget is the mailbox a channel switched back to SMTP left behind,
	// for Forget once the transaction has committed.
	var forget string
	txErr := s.withTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if err := lockDefaultChannelSlot(ctx, txq); err != nil {
			return err
		}

		// RE-READ UNDER THE LOCK, and write only values derived from THIS
		// read. `existing` above is fetched through the pool at step 2,
		// before this transaction begins, and the window between the two is
		// not small: it contains the credential lookup, a secret open, a
		// secret seal and a JSON marshal.
		//
		// This is the fourth defect ux_channels_type_is_default has produced,
		// and the one the advisory lock alone does NOT close. Serialising the
		// transactions does not refresh a value captured before the
		// transaction started, so a PUT that says nothing about the default —
		// `{"isActive": true}` — against the row that *was* the default would
		// write is_default = true back from its stale snapshot, after a
		// concurrent create had already demoted it. Two default rows, and a
		// 23505 that escapes as a bare RFC 7807 409 on an operation whose
		// contract declares no 409. The lock is not even load-bearing for it:
		// the same collision happens uncontended whenever a create commits
		// inside that window.
		//
		// Every field below comes from `current` for the same reason — a
		// stale display name would silently revert a concurrent rename, and a
		// stale is_active a concurrent deactivation. `existing` is still what
		// the 404, the validation order and the credential decisions above
		// are based on, which keeps those observable orders exactly as
		// inventory §19.2 pins them; only the values actually WRITTEN are
		// re-read. .NET reads its entity once and saves it tracked, so the
		// re-read is a deliberate divergence: it is what makes the write
		// correct under concurrency, and it cannot change a single-writer
		// outcome, since with no concurrent writer the two reads agree.
		current, err := txq.GetChannelByID(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted between step 2's lookup and this transaction. Answer
			// the same bare 404 step 2 would have.
			return errChannelVanished
		} else if err != nil {
			return fmt.Errorf("communications: re-read channel under the lock: %w", err)
		}
		// Every decision above was made on `existing`'s provider; a switch
		// committed in between would make them wrong (a credential written
		// onto a Microsoft 365 channel, or a mailbox never forgotten).
		if current.Provider != existing.Provider {
			return errChannelProviderChanged
		}

		isDefault := current.IsDefault
		if wantDefault {
			if err := txq.ClearOtherDefaultChannels(ctx, req.Id); err != nil {
				return err
			}
			isDefault = true
		}
		isActive := current.IsActive
		if body.IsActive != nil {
			isActive = *body.IsActive
		}
		finalDisplayName := current.DisplayName
		switch {
		case clearDisplayName:
			finalDisplayName = nil
		case newDisplayName != nil:
			finalDisplayName = newDisplayName
		}
		updated, err := txq.UpdateChannel(ctx, store.UpdateChannelParams{
			ID: req.Id, DisplayName: finalDisplayName, IsActive: isActive, IsDefault: isDefault,
		})
		if err != nil {
			return err
		}
		if backToSMTP {
			if forget, err = channelMailboxOf(current.Settings); err != nil {
				return err
			}
			if err := txq.SetChannelSmtp(ctx, req.Id); err != nil {
				return err
			}
			updated.Provider, updated.Settings = providerSMTP, []byte("{}")
		}
		settingsJSON := current.CredentialSettingsJson
		if rewriteCredential {
			// THE PASSWORD MERGE, here rather than before the transaction,
			// and against `current` rather than `existing`. A reuse merge
			// built on a pre-transaction read is a silent lost update: the
			// stored password a concurrent PUT had just rotated gets sealed
			// again from the stale snapshot and written back over the new
			// one. Nothing detects it — no constraint covers a credential
			// blob, so there is no violation to catch, no error and no log —
			// which is why the only detector is
			// TestUpdateChannel_ConcurrentCredentialWritesDoNotRevertARotatedPassword.
			plain := ""
			switch {
			case suppliedPassword != nil:
				plain = *suppliedPassword
			case current.CredentialSettingsJson != nil:
				cred, cerr := txq.GetChannelCredentialByChannelID(ctx, req.Id)
				if cerr != nil {
					return fmt.Errorf("communications: get channel credential: %w", cerr)
				}
				// A ciphertext that fails to open (wrong purpose, tampered, a
				// rotated APP_SECRET) silently becomes an empty password rather
				// than an error — the same swallow-the-failure behaviour
				// inventory §15.6 documents for .NET's TryUpdateCredential.
				if sealed, derr := decodeCiphertext(cred.SecretCiphertext); derr == nil {
					if opened, oerr := s.deps.Secrets.Open(channelCredentialPurpose, sealed); oerr == nil {
						plain = string(opened)
					}
				}
			}
			sealed, serr := s.deps.Secrets.Seal(channelCredentialPurpose, []byte(plain))
			if serr != nil {
				return fmt.Errorf("communications: seal channel credential: %w", serr)
			}
			// Upsert, not a bare UPDATE: a channel can reach here with no
			// credential row at all (TestUpdateChannel_MissingCredentialWhenNoneExists
			// deletes one directly to exercise the hazard, and the credErrs
			// branch above only refuses that case when body.Smtp is *absent* —
			// a present, valid smtp block must still persist). A plain
			// UPDATE ... WHERE channel_id = ... matches zero rows here and
			// silently discards the write; fix round 1 caught this live: 200
			// with hasCredentials:true, then a GET showing hasCredentials:false.
			if err := txq.UpsertChannelCredential(ctx, store.UpsertChannelCredentialParams{
				ID: uuid.New(), ChannelID: req.Id, SettingsJson: newSettingsJSON,
				SecretCiphertext: encodeCiphertext(sealed), CreatedAt: now,
			}); err != nil {
				return err
			}
			settingsJSON = &newSettingsJSON
		}
		resp, err = channelResponseOf(channelRowFromChannel(updated), settingsJSON)
		return err
	})
	if txErr != nil {
		if errors.Is(txErr, errChannelVanished) {
			return gen.PutCommunicationsChannelsById404Response{}, nil
		}
		if errors.Is(txErr, errChannelProviderChanged) {
			return gen.PutCommunicationsChannelsById400JSONResponse(validationErrorBody(map[string][]string{
				"provider": {"The channel's provider changed while this request was made. Read the channel again and retry."},
			})), nil
		}
		return nil, fmt.Errorf("communications: update channel: %w", txErr)
	}
	if forget != "" {
		s.forgetMailbox(ctx, req.Id, forget)
	}
	return gen.PutCommunicationsChannelsById200JSONResponse(resp), nil
}

// errChannelProviderChanged reports that a channel's provider was switched
// between PutChannelById's lookup and its transaction's re-read: the
// request's credential decisions were made on the old one.
var errChannelProviderChanged = errors.New("communications: the channel's provider changed under the request")

// forgetMailbox drops the mailbox a channel switched back to SMTP no longer
// uses, after the switch committed (M11): best-effort — the channel is SMTP
// whatever the provider answers, and a mailbox left recorded only lets the
// provider send through one an admin checked — and logged when it fails.
func (s *server) forgetMailbox(ctx context.Context, channelID uuid.UUID, mailbox string) {
	m, err := s.channelMailbox(ctx)
	if err == nil && m != nil {
		err = s.mailboxForget(ctx, m, mailbox)
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "communications: forget the channel's mailbox after its switch back to SMTP",
			"channel_id", channelID, "mailbox", mailbox, "err", err)
	}
}

// channelDefaultLockClass namespaces lockDefaultChannelSlot's advisory-lock
// key away from every other pg_advisory_xact_lock caller in the
// installation. It uses the two-int32 overload, exactly as energy's
// lockSupplyPeriods does and for the same reason: retention's
// CommunicationsAdvisoryLease and internal/db's migration lock both use the
// single-bigint overload, which is a separate key space, and a collision
// inside this overload would need another caller's class to equal this one.
const channelDefaultLockClass = 0x43484E44 // "CHND", arbitrary but memorable

// errChannelVanished reports that the channel disappeared between
// PutChannelById's step-2 lookup and its transaction's re-read under the
// lock. It is mapped to the same bare 404 step 2 answers, never to a 500: a
// row deleted concurrently is "not found", whichever read noticed.
var errChannelVanished = errors.New("communications: the channel no longer exists")

// lockDefaultChannelSlot takes the transaction-scoped advisory lock that
// serialises every writer able to change which channel is the default. See
// queries/channels.sql's LockDefaultChannelSlot for why a lock rather than a
// smarter statement or a caught violation: the PUT-vs-POST pair collides on a
// row that does not exist yet, which no predicate can visit and no row lock
// can reach, and the only other remedy would add a 409 this operation's
// contract does not declare.
//
// ONE GLOBAL SLOT, not one per channel type: both demote statements are
// type-unfiltered (faithfully — .NET's are too), so the write set this
// serialises is every channel row, and a type-keyed lock would be
// under-scoped. That query's comment carries the full reasoning. Released
// automatically at commit or rollback, so no path can leak it.
//
// The lock is necessary and NOT sufficient on its own: it serialises
// transactions but cannot refresh a value read before one began, which is
// why PutChannelById re-reads both the channel row and its credential
// inside the transaction and derives every written value from those reads.
// That "serialise, then read fresh" protocol depends on READ COMMITTED —
// see the query's comment for why a higher isolation level would silently
// reopen the pairs this closes.
func lockDefaultChannelSlot(ctx context.Context, q *store.Queries) error {
	if err := q.LockDefaultChannelSlot(ctx, channelDefaultLockClass); err != nil {
		return fmt.Errorf("communications: lock the default-channel slot: %w", err)
	}
	return nil
}

// smtpTLSMode is the TLS-mode half of SmtpDeliveryProvider.ExecuteAsync's
// decision tree (inventory §15.2 item 3), as far as this port supports it:
// no host-config-driven "Development + AllowInsecurePlaintext" escape
// hatch — channel verification always demands a real answer from the
// destination. useSsl -> implicit TLS; port 587 without useSsl -> STARTTLS;
// anything else is refused before any connection is attempted, the same
// refusal .NET's ExecuteAsync throws and VerifyChannel's generic catch
// turns into 422 verification_failed.
func smtpTLSMode(useSsl bool, port int32) (string, error) {
	switch {
	case useSsl:
		return "implicit", nil
	case port == 587:
		return "starttls", nil
	default:
		return "", fmt.Errorf("communications: smtp tls is required: set useSsl for implicit TLS, or use port 587 for STARTTLS")
	}
}

// verifyChannel is VerifyAsync (inventory §15.2): the same settings
// resolution, timeout invariant, TLS decision, destination guard, connect
// and auth as a real send — and then immediately disconnects, never
// building or sending a message. See this file's header note for the
// Mailgun-branch decision: a non-SMTP channel.Provider is refused here
// defensively; this API's own create/update validation never lets one
// exist in the first place. The actual connectivity check goes through
// Deps.SMTPVerify (module.Deps' doc has the full reasoning) — nil in
// production, meaning mail.VerifyConnection itself, so production always
// dials through internal/mail's real destination guard; only a test
// harness ever substitutes a fake.
func (s *server) verifyChannel(ctx context.Context, q *store.Queries, channel store.GetChannelByIDRow) error {
	if !strings.EqualFold(channel.Provider, "smtp") {
		return errUnsupportedChannelProvider
	}
	if channel.CredentialSettingsJson == nil {
		return errChannelMissingCredentials
	}
	var settings smtpProviderSettings
	if err := json.Unmarshal([]byte(*channel.CredentialSettingsJson), &settings); err != nil {
		return fmt.Errorf("communications: decode channel settings: %w", err)
	}
	cred, err := q.GetChannelCredentialByChannelID(ctx, channel.ID)
	if err != nil {
		return fmt.Errorf("communications: get channel credential: %w", err)
	}
	password := ""
	if sealed, derr := decodeCiphertext(cred.SecretCiphertext); derr == nil {
		if opened, oerr := s.deps.Secrets.Open(channelCredentialPurpose, sealed); oerr == nil {
			password = string(opened)
		}
	}
	tlsMode, err := smtpTLSMode(settings.UseSsl, settings.Port)
	if err != nil {
		return err
	}
	username := ""
	if settings.Username != nil {
		username = *settings.Username
	}

	verify := s.deps.SMTPVerify
	if verify == nil {
		verify = mail.VerifyConnection
	}
	verifyCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	return verify(verifyCtx, config.MailConfig{
		Driver: "smtp", Host: settings.Host, Port: int(settings.Port),
		Username: username, Password: password, From: channel.Address, TLS: tlsMode,
	})
}

// PostCommunicationsChannelsByIdVerify Verify a channel
// (POST /api/v1/communications/channels/{id}/verify)
//
// VerifyChannel (inventory §2): lookup -> 404 bare, before anything else;
// then the provider dispatch above, with a 10s timeout; any
// SmtpDestinationRejectedException-equivalent -> 422 destination_rejected
// with the guard's own message, any other failure -> 422
// verification_failed with a generic one. There is no request body, so
// nothing to validate before the lookup.
func (s *server) PostCommunicationsChannelsByIdVerify(ctx context.Context, req gen.PostCommunicationsChannelsByIdVerifyRequestObject) (gen.PostCommunicationsChannelsByIdVerifyResponseObject, error) {
	q := store.New(s.deps.Pool)
	channel, err := q.GetChannelByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsChannelsByIdVerify404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get channel: %w", err)
	}

	if channel.Provider == providerEntra {
		return s.verifyMailboxChannel(ctx, channel)
	}
	verifyErr := s.verifyChannel(ctx, q, channel)
	if verifyErr == nil {
		return gen.PostCommunicationsChannelsByIdVerify200JSONResponse(gen.ChannelVerifyResponse{Ok: true}), nil
	}
	if errors.Is(verifyErr, mail.ErrDestinationRejected) {
		return gen.PostCommunicationsChannelsByIdVerify422JSONResponse(flatErrorBody(
			"destination_rejected", verifyErr.Error())), nil
	}
	return gen.PostCommunicationsChannelsByIdVerify422JSONResponse(flatErrorBody(
		"verification_failed", "Channel verification failed.")), nil
}

// verifyMailboxChannel is verify for a Microsoft 365 channel (M12): Status
// and Check of the channel's address again, ok only when it still reaches a
// shared mailbox with the object id the channel holds. Every refusal is a
// 422 with the create's code; mailbox_changed when the address now names
// another mailbox.
func (s *server) verifyMailboxChannel(ctx context.Context, channel store.GetChannelByIDRow) (gen.PostCommunicationsChannelsByIdVerifyResponseObject, error) {
	stored, err := channelMailboxOf(channel.Settings)
	if err != nil {
		return nil, err
	}
	info, refusal, err := s.resolveSharedMailbox(ctx, channel.Address)
	if err != nil {
		return nil, err
	}
	if refusal == nil && !strings.EqualFold(info.Mailbox, stored) {
		refusal = refusalMailboxChanged
	}
	if refusal != nil {
		return gen.PostCommunicationsChannelsByIdVerify422JSONResponse(flatErrorBody(refusal.code, refusal.message)), nil
	}
	return gen.PostCommunicationsChannelsByIdVerify200JSONResponse(gen.ChannelVerifyResponse{Ok: true}), nil
}

// errProviderUnchanged is the switch's refusal of a channel already on
// Microsoft 365, found by the re-read under the lock.
var errProviderUnchanged = errors.New("communications: the channel already uses the provider")

// PostCommunicationsChannelsByIdProvider Switch a channel's provider
// (POST /api/v1/communications/channels/{id}/provider)
//
// The switch of an SMTP channel to Microsoft 365 (M12): (1) the body names
// entra -> 400; (2) the lookup -> 404 bare; (3) already entra -> 422
// provider_unchanged; (4) Status and Check of the channel's own address,
// before the transaction -> the create's refusals; (5) under
// lockDefaultChannelSlot and a re-read, the provider and the mailbox written
// and the credential row deleted in ONE transaction, so a failure leaves the
// channel SMTP with its credential. The id, the address, the conversations
// and the default flag stay. The mailbox Check recorded stays recorded if the
// transaction then fails: it only lets the provider send through a mailbox an
// admin checked, and the next Check rewrites it.
func (s *server) PostCommunicationsChannelsByIdProvider(ctx context.Context, req gen.PostCommunicationsChannelsByIdProviderRequestObject) (gen.PostCommunicationsChannelsByIdProviderResponseObject, error) {
	if req.Body == nil || strings.ToLower(strings.TrimSpace(req.Body.Provider)) != providerEntra {
		return gen.PostCommunicationsChannelsByIdProvider400JSONResponse(validationErrorBody(map[string][]string{
			"provider": {"A channel is switched to the entra provider only."},
		})), nil
	}

	existing, err := store.New(s.deps.Pool).GetChannelByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsChannelsByIdProvider404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get channel: %w", err)
	}
	if existing.Provider == providerEntra {
		return switchProviderUnchanged(), nil
	}

	info, refusal, err := s.resolveSharedMailbox(ctx, existing.Address)
	if err != nil {
		return nil, err
	}
	if refusal != nil {
		body := flatErrorBody(refusal.code, refusal.message)
		switch refusal.status {
		case http.StatusConflict:
			return gen.PostCommunicationsChannelsByIdProvider409JSONResponse(body), nil
		case http.StatusServiceUnavailable:
			return gen.PostCommunicationsChannelsByIdProvider503JSONResponse(body), nil
		default:
			return gen.PostCommunicationsChannelsByIdProvider422JSONResponse(body), nil
		}
	}
	settings, err := json.Marshal(mailboxSettings{Mailbox: info.Mailbox})
	if err != nil {
		return nil, fmt.Errorf("communications: encode channel settings: %w", err)
	}

	var resp gen.ChannelResponse
	txErr := s.withTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		if err := lockDefaultChannelSlot(ctx, txq); err != nil {
			return err
		}
		current, err := txq.GetChannelByID(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errChannelVanished
		} else if err != nil {
			return fmt.Errorf("communications: re-read channel under the lock: %w", err)
		}
		if current.Provider == providerEntra {
			return errProviderUnchanged
		}
		updated, err := txq.SwitchChannelProvider(ctx, store.SwitchChannelProviderParams{
			ID: req.Id, Provider: providerEntra, Settings: settings,
		})
		if err != nil {
			return err
		}
		if err := txq.DeleteChannelCredential(ctx, req.Id); err != nil {
			return err
		}
		resp, err = channelResponseOf(channelRowFromChannel(updated), nil)
		return err
	})
	switch {
	case txErr == nil:
		return gen.PostCommunicationsChannelsByIdProvider200JSONResponse(resp), nil
	case errors.Is(txErr, errChannelVanished):
		return gen.PostCommunicationsChannelsByIdProvider404Response{}, nil
	case errors.Is(txErr, errProviderUnchanged):
		return switchProviderUnchanged(), nil
	case db.IsUniqueViolation(txErr, "ux_channels_entra_mailbox"):
		return gen.PostCommunicationsChannelsByIdProvider409JSONResponse(flatErrorBody(
			"channel_exists", "A channel for this mailbox already exists.")), nil
	default:
		return nil, fmt.Errorf("communications: switch channel provider: %w", txErr)
	}
}

func switchProviderUnchanged() gen.PostCommunicationsChannelsByIdProviderResponseObject {
	return gen.PostCommunicationsChannelsByIdProvider422JSONResponse(flatErrorBody(
		"provider_unchanged", "The channel already uses Microsoft 365."))
}

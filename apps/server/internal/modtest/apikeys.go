package modtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"math/big"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// An integration and its API keys (API access design K8, K11), written as
// identity's integrations service writes them, for a module's tests to call
// the module as an external application would.

// GrantIntegrationRoles gives the integration roleIDs on tx, under the licence
// identity's integrations service sets on its own transaction
// (vantigo.integration_roles, 00194): without it the database refuses an
// integration's role (K3, K55). Every test that gives an integration a role
// goes through it.
func GrantIntegrationRoles(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, roleIDs ...uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, integrationID.String()); err != nil {
		return fmt.Errorf("modtest: licence the integration's roles: %w", err)
	}
	for _, role := range roleIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, integrationID, role); err != nil {
			return fmt.Errorf("modtest: give the integration a role: %w", err)
		}
	}
	return nil
}

// Integration seeds an integration named name: its user of kind integration,
// its row, and, when permissions are given, a custom role of its own holding
// them. It answers the integration's user id.
func (h *Harness) Integration(t testing.TB, name string, permissions ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := "integration-" + id.String() + "@integrations.invalid"
	now := h.Now()
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), true, $3, 'integration', $4, $5, $5)`, id, email, name, uuid.New(), now)
	h.Exec(t, `INSERT INTO identity.integrations (user_id, name, created_at, version) VALUES ($1, $2, $3, $4)`, id, name, now, uuid.New())
	if len(permissions) > 0 {
		role := h.seedRole(t, permissions)
		if err := pgx.BeginFunc(context.Background(), h.pool, func(tx pgx.Tx) error {
			return GrantIntegrationRoles(context.Background(), tx, id, role)
		}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// APIKey is one key APIKeyFor mints: Permissions is its narrowing (nil for
// none), ReadOnly makes it call only reads, Networks limit where it is used
// from, and Expires is its expiry (90 days from the harness clock when zero).
type APIKey struct {
	Permissions []string
	ReadOnly    bool
	Networks    []netip.Prefix
	Expires     time.Time
}

// APIKeyFor mints a key for the integration as identity does — vtg_, the 32
// random bytes in base62 left-padded to 43, the CRC32 of those in base62
// left-padded to 6, stored as the SHA-256 of the bytes
// (internal/identity/apikey.go) — and answers the key and its id.
func (h *Harness) APIKeyFor(t testing.TB, integration uuid.UUID, k APIKey) (string, uuid.UUID) {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("modtest: %v", err)
	}
	random := base62(new(big.Int).SetBytes(raw), 43)
	key := "vtg_" + random + base62(new(big.Int).SetUint64(uint64(crc32.ChecksumIEEE([]byte(random)))), 6)
	hash := sha256.Sum256(raw)
	expires := k.Expires
	if expires.IsZero() {
		expires = h.Now().AddDate(0, 0, 90)
	}
	networks := k.Networks
	if networks == nil {
		networks = []netip.Prefix{}
	}
	id := uuid.New()
	h.Exec(t, `INSERT INTO identity.api_keys (id, integration_id, name, token_hash, display, permissions, read_only, allowed_networks, expires_at, created_at)
	           VALUES ($1, $2, 'Modtest', $3, $4, $5, $6, $7, $8, $9)`,
		id, integration, hash[:], key[:8]+"…"+key[49:], k.Permissions, k.ReadOnly, networks, expires, h.Now())
	return key, id
}

// APIKeyClient is a client that presents key on every request, as an
// external application does: Authorization: Bearer vtg_….
func (h *Harness) APIKeyClient(t testing.TB, key string) *Client {
	t.Helper()
	c := h.Client(t)
	c.header = http.Header{"Authorization": {"Bearer " + key}}
	return c
}

// SignInAPIKey is SignInUser for an integration: a new integration holding
// exactly permissions through a role of its own, and a client presenting a
// key of it shaped by k. It answers the integration's user id too.
func (h *Harness) SignInAPIKey(t testing.TB, k APIKey, permissions ...string) (*Client, uuid.UUID) {
	t.Helper()
	id := h.Integration(t, fmt.Sprintf("modtest-integration-%d", h.users.Add(1)), permissions...)
	key, _ := h.APIKeyFor(t, id, k)
	return h.APIKeyClient(t, key), id
}

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// base62 is n in base62, left-padded with zeros to width.
func base62(n *big.Int, width int) string {
	digits := make([]byte, width)
	v := new(big.Int).Set(n)
	mod := new(big.Int)
	sixtyTwo := big.NewInt(62)
	for i := width - 1; i >= 0; i-- {
		v.DivMod(v, sixtyTwo, mod)
		digits[i] = base62Alphabet[mod.Int64()]
	}
	return string(digits)
}

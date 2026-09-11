package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// totpSecretPurpose is the secrets.Box purpose a TOTP secret is sealed
// under at rest.
const totpSecretPurpose = "identity/totp"

// TOTP's parameters (spec *Credentials*): HMAC-SHA1, six digits, 30-second
// steps and one step of skew either side, the parameters of ASP.NET's
// authenticator provider, which .NET's test generator confirms
// (TS/Integration/IdentityApiFactory.cs:234-250). A secret is 20 random
// bytes, as ASP.NET's authenticator key was.
const (
	totpPeriod      = 30
	totpSkew        = 1
	totpSecretBytes = 20
)

// totpStepOpts validates a code against exactly one step: totpStep walks
// the skew window itself, so that it knows which step a code belongs to.
var totpStepOpts = totp.ValidateOpts{
	Period:    totpPeriod,
	Skew:      0,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

var totpSecretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a new authenticator secret: 20 random bytes as
// unpadded base32, the text an authenticator app is given.
func newTOTPSecret() string {
	b := make([]byte, totpSecretBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return totpSecretEncoding.EncodeToString(b)
}

// sealTOTPSecret encrypts secret for identity.users.totp_secret.
func (s *server) sealTOTPSecret(secret string) ([]byte, error) {
	sealed, err := s.deps.Secrets.Seal(totpSecretPurpose, []byte(secret))
	if err != nil {
		return nil, fmt.Errorf("identity: seal TOTP secret: %w", err)
	}
	return sealed, nil
}

// verifyTOTP checks code against the sealed secret at now and returns the
// time step it belongs to. It reports false for an account without a
// secret, and for a code that matches no step within the skew window; the
// caller still has to spend the step (RecordTOTPStep, EnableTOTP), which
// is what rejects a replay.
func (s *server) verifyTOTP(sealed []byte, code string, now time.Time) (step int64, ok bool, err error) {
	if sealed == nil || code == "" {
		return 0, false, nil
	}
	secret, err := s.deps.Secrets.Open(totpSecretPurpose, sealed)
	if err != nil {
		return 0, false, fmt.Errorf("identity: open TOTP secret: %w", err)
	}
	step, ok = totpStep(string(secret), code, now)
	return step, ok, nil
}

// totpStep returns the step within totpSkew of now whose code is code. It
// tries the latest step first, so a code that happens to match two steps
// spends the later one. The library compares codes in constant time.
func totpStep(secret, code string, now time.Time) (int64, bool) {
	current := now.Unix() / totpPeriod
	for step := current + totpSkew; step >= current-totpSkew; step-- {
		ok, err := totp.ValidateCustom(code, secret, time.Unix(step*totpPeriod, 0), totpStepOpts)
		if err == nil && ok {
			return step, true
		}
	}
	return 0, false
}

// twoFactorCode is a submitted code as it is checked: without spaces, as
// .NET stripped them (EA/AuthEndpoints.cs:388, EA/AuthAccountEndpoints.cs:210).
func twoFactorCode(code *string) string {
	return strings.ReplaceAll(deref(code), " ", "")
}

// isTOTPCode reports whether code has a TOTP code's shape: exactly six
// digits. Any other code is a recovery code (EA/AuthEndpoints.cs:389-393).
func isTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range []byte(code) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Recovery codes (spec *Credentials*): ten per set, each two groups of five
// characters from an alphabet without the easily confused 0, 1, I and O.
// Its 32 letters make every random byte's low five bits an unbiased pick.
const (
	recoveryCodeCount    = 10
	recoveryCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// newRecoveryCodes returns a fresh set of distinct recovery codes, formatted
// XXXXX-XXXXX, and the hashes to store for them.
func newRecoveryCodes() (codes []string, hashes [][]byte) {
	seen := map[string]bool{}
	for len(codes) < recoveryCodeCount {
		b := make([]byte, 10)
		_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
		var sb strings.Builder
		for i, v := range b {
			if i == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryCodeAlphabet[v&31])
		}
		code := sb.String()
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
		hashes = append(hashes, hashRecoveryCode(code))
	}
	return codes, hashes
}

// hashRecoveryCode is what identity stores for a recovery code: SHA-256 of
// the code upper-cased and without its dash, so that the comparison ignores
// both case and the dash. Only ASCII letters are upper-cased: no other
// character can stand for one of the alphabet's.
func hashRecoveryCode(code string) []byte {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case r == '-':
			return -1
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		default:
			return r
		}
	}, code)
	sum := sha256.Sum256([]byte(normalized))
	return sum[:]
}

// replaceRecoveryCodes gives userID a fresh set of recovery codes on q, the
// caller's transaction, and returns them: the only time they are ever
// shown. Every code of the old set stops working.
func replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID uuid.UUID) ([]string, error) {
	codes, hashes := newRecoveryCodes()
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, fmt.Errorf("identity: recovery codes: %w", err)
	}
	if err := q.InsertRecoveryCodes(ctx, store.InsertRecoveryCodesParams{UserID: userID, CodeHashes: hashes}); err != nil {
		return nil, fmt.Errorf("identity: recovery codes: %w", err)
	}
	return codes, nil
}

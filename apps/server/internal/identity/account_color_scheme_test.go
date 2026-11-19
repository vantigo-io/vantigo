package identity_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The personal colour scheme (dark theme design D-A): the profile sets it and
// clears it, the account answers the stored choice, and leaving it out keeps
// it, on PATCH and on PUT alike, so a client that predates the field never
// clears it.
func TestAccountProfile_ColorSchemeIsSetNormalisedClearedAndKeptWhenLeftOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "scheme@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	if a := getAccount(t, c); a.ColorScheme != nil {
		t.Errorf("a new account's colorScheme = %q, want null (follow the computer)", *a.ColorScheme)
	}

	put := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": "Scheme", "colorScheme": " Dark "})
	var putAccount account
	put.json(&putAccount)
	if put.status != http.StatusOK || putAccount.ColorScheme == nil || *putAccount.ColorScheme != "dark" {
		t.Fatalf("PUT dark: status %d body %s, want 200 and dark trimmed and lowercased", put.status, put.body)
	}
	if a := getAccount(t, c); a.ColorScheme == nil || *a.ColorScheme != "dark" {
		t.Errorf("GET %s colorScheme = %v, want dark", accountPath, a.ColorScheme)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND color_scheme = 'dark'`, id); got != 1 {
		t.Errorf("color_scheme in the database is not dark")
	}

	// Leaving it out keeps it.
	if r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Scheme", "preferredLanguage": "nb"}); r.status != http.StatusOK {
		t.Fatalf("PATCH without colorScheme: status %d body %s", r.status, r.body)
	}
	if r := c.do(http.MethodPut, accountPath, map[string]any{"displayName": "Scheme"}); r.status != http.StatusOK {
		t.Fatalf("PUT without colorScheme: status %d body %s", r.status, r.body)
	}
	if a := getAccount(t, c); a.ColorScheme == nil || *a.ColorScheme != "dark" {
		t.Errorf("colorScheme after edits that left it out = %v, want dark kept", a.ColorScheme)
	}

	if r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Scheme", "colorScheme": "light"}); r.status != http.StatusOK {
		t.Fatalf("PATCH light: status %d body %s", r.status, r.body)
	}
	if a := getAccount(t, c); a.ColorScheme == nil || *a.ColorScheme != "light" {
		t.Errorf("colorScheme = %v, want light", a.ColorScheme)
	}

	// Automatic, in any case, a blank value and null all clear it.
	for _, value := range []any{"automatic", " AUTOMATIC ", "", nil} {
		if r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Scheme", "colorScheme": "dark"}); r.status != http.StatusOK {
			t.Fatalf("PATCH dark: status %d body %s", r.status, r.body)
		}
		r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Scheme", "colorScheme": value})
		if r.status != http.StatusOK {
			t.Fatalf("PATCH colorScheme %v: status %d body %s", value, r.status, r.body)
		}
		if a := getAccount(t, c); a.ColorScheme != nil {
			t.Errorf("colorScheme after %v = %q, want null", value, *a.ColorScheme)
		}
	}
}

// Anything but light, dark or automatic is refused on colorScheme in the
// profile's own invalid_request shape, with nothing written, and together
// with the other fields' refusals.
func TestAccountProfile_ColorSchemeRefusesOtherValuesWithoutWriting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "badscheme@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)
	if r := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": "Before", "colorScheme": "dark"}); r.status != http.StatusOK {
		t.Fatalf("PUT dark: status %d body %s", r.status, r.body)
	}

	for _, value := range []any{"blue", "auto", "system", "dark;", 42} {
		for _, method := range []string{http.MethodPut, http.MethodPatch} {
			var opts []reqOpt
			if _, isString := value.(string); !isString {
				opts = append(opts, skipContract("a number where the contract names a string is the request under test"))
			}
			r := c.do(method, accountProfilePath, map[string]any{"displayName": "After", "colorScheme": value}, opts...)
			if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
				t.Errorf("%s colorScheme %v: status %d code %q body %s, want 400 invalid_request", method, value, r.status, r.code(), r.body)
				continue
			}
			if msgs := fieldErrors(t, r)["colorScheme"]; len(msgs) == 0 {
				t.Errorf("%s colorScheme %v: fields %v, want one on colorScheme", method, value, fieldErrors(t, r))
			}
		}
	}
	r := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": " ", "colorScheme": "blue"})
	if fields := fieldErrors(t, r); len(fields["displayName"]) == 0 || len(fields["colorScheme"]) == 0 {
		t.Errorf("a blank name and blue: fields %v, want both refused", fields)
	}

	a := getAccount(t, c)
	if a.DisplayName != "Before" || a.ColorScheme == nil || *a.ColorScheme != "dark" {
		t.Errorf("after the refusals: displayName %q colorScheme %v, want Before and dark untouched", a.DisplayName, a.ColorScheme)
	}
}

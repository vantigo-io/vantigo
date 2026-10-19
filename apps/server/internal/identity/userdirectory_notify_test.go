package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// bilingual is a notice in both languages a module writes.
var bilingual = contracts.Notice{Texts: map[string]contracts.NoticeText{
	"en": {Subject: "Kari Nordmann invited you: Customer meeting", Body: "Open it in Vantigo: https://app.example.test/calendar"},
	"nb": {Subject: "Kari Nordmann inviterte deg: Kundemøte", Body: "Åpne den i Vantigo: https://app.example.test/calendar"},
}}

// notifyUser inserts an active user at email with the given preferred
// language ("" for none) and returns its id.
func notifyUser(t *testing.T, h *harness, email, language string) uuid.UUID {
	t.Helper()
	id := h.insertUser(t, email)
	if language != "" {
		h.exec(t, `UPDATE identity.users SET preferred_language = $2 WHERE id = $1`, id, language)
	}
	return id
}

// TestUserDirectory_NotifySendsInThePreferredLanguage proves an active user is
// mailed at identity's address for them, in the language they chose, as a
// plain text mail.
func TestUserDirectory_NotifySendsInThePreferredLanguage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := notifyUser(t, h, "kari@example.test", "nb")

	if err := newUserDirectory(t, h).Notify(context.Background(), id, bilingual); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := h.mailTo("kari@example.test")
	if len(got) != 1 || got[0].Subject != bilingual.Texts["nb"].Subject || got[0].TextBody != bilingual.Texts["nb"].Body {
		t.Errorf("mail to kari = %+v, want the Norwegian notice once", got)
	}
}

// TestUserDirectory_NotifyFallsBackToEnglishThenAny proves the choice when the
// user's own language is not on the notice, or they chose none: English, and
// without English whichever text there is.
func TestUserDirectory_NotifyFallsBackToEnglishThenAny(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newUserDirectory(t, h)
	ctx := context.Background()
	english := contracts.Notice{Texts: map[string]contracts.NoticeText{"en": bilingual.Texts["en"]}}
	norwegian := contracts.Notice{Texts: map[string]contracts.NoticeText{"nb": bilingual.Texts["nb"]}}

	for _, tc := range []struct {
		name, email, language string
		notice                contracts.Notice
		want                  contracts.NoticeText
	}{
		{"no preference, both texts", "none@example.test", "", bilingual, bilingual.Texts["en"]},
		{"Norwegian preferred, English only", "nb-en@example.test", "nb", english, bilingual.Texts["en"]},
		{"English preferred, Norwegian only", "en-nb@example.test", "en", norwegian, bilingual.Texts["nb"]},
	} {
		id := notifyUser(t, h, tc.email, tc.language)
		if err := dir.Notify(ctx, id, tc.notice); err != nil {
			t.Fatalf("%s: Notify: %v", tc.name, err)
		}
		got := h.mailTo(tc.email)
		if len(got) != 1 || got[0].Subject != tc.want.Subject || got[0].TextBody != tc.want.Body {
			t.Errorf("%s: mail = %+v, want %q once", tc.name, got, tc.want.Subject)
		}
	}
}

// TestUserDirectory_NotifySendsNothingToWhomCannotAct proves a disabled
// user, a pending one, an unknown id and a user whose only address is a
// placeholder no mail reaches are not mailed, and that none of them is an
// error; nor is a notice with no text.
func TestUserDirectory_NotifySendsNothingToWhomCannotAct(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newUserDirectory(t, h)
	ctx := context.Background()

	disabled := notifyUser(t, h, "disabled@example.test", "")
	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'owner' WHERE id = $1`, disabled)
	pending := notifyUser(t, h, "pending@example.test", "")
	h.exec(t, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'E-1', employee_number_key = 'E-1', pending_since = $2 WHERE id = $1`, pending, h.now())
	placeholder := notifyUser(t, h, "scim-"+uuid.NewString()+"@sso.invalid", "")
	active := notifyUser(t, h, "active@example.test", "")

	for name, id := range map[string]uuid.UUID{"disabled": disabled, "pending": pending, "unknown": uuid.New(), "placeholder": placeholder} {
		if err := dir.Notify(ctx, id, bilingual); err != nil {
			t.Errorf("Notify(%s) = %v, want nil", name, err)
		}
	}
	if err := dir.Notify(ctx, active, contracts.Notice{}); err != nil {
		t.Errorf("Notify(no text) = %v, want nil", err)
	}
	if got := h.mail.Messages(); len(got) != 0 {
		t.Errorf("mails = %+v, want none", got)
	}
}

// TestUserDirectory_NotifyFailureNamesNoAddress proves a send the driver
// refuses is an error, and that the error carries neither the address nor
// the driver's own words, which can name the recipient.
func TestUserDirectory_NotifyFailureNamesNoAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := notifyUser(t, h, "kari@example.test", "")
	h.mail.FailNext(errors.New("550 5.1.1 <kari@example.test>: mailbox unavailable"))

	err := newUserDirectory(t, h).Notify(context.Background(), id, bilingual)
	if err == nil {
		t.Fatal("Notify = nil, want the failed send's error")
	}
	if strings.Contains(err.Error(), "kari") || strings.Contains(err.Error(), "@") {
		t.Errorf("Notify error = %q, want one naming no address", err)
	}
	if !strings.Contains(err.Error(), id.String()) {
		t.Errorf("Notify error = %q, want it to name the user", err)
	}
}

// TestUserDirectory_NotifyWithoutAMailDriverIsNotAnError proves an
// installation composed with no mail sender answers a notice as not sent,
// never as a failure.
func TestUserDirectory_NotifyWithoutAMailDriverIsNotAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := notifyUser(t, h, "kari@example.test", "nb")
	deps := h.deps
	deps.Mail = nil

	if err := identity.Module(h.access).Users(deps).Notify(context.Background(), id, bilingual); err != nil {
		t.Errorf("Notify = %v, want nil", err)
	}
}

// TestUserDirectory_EntriesCarryThePreferredLanguage proves User, Users and
// SearchUsers answer the language the user chose, and "" for one who chose
// none.
func TestUserDirectory_EntriesCarryThePreferredLanguage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newUserDirectory(t, h)
	ctx := context.Background()
	nb := insertUserNamed(t, h, "Language Norsk", false)
	h.exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, nb)
	none := insertUserNamed(t, h, "Language None", false)

	user, err := dir.User(ctx, nb)
	if err != nil || user == nil || user.PreferredLanguage != "nb" {
		t.Errorf("User = %+v, %v, want PreferredLanguage nb", user, err)
	}
	users, err := dir.Users(ctx, []uuid.UUID{nb, none})
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, u := range users {
		got[u.ID] = u.PreferredLanguage
	}
	if len(got) != 2 || got[nb] != "nb" || got[none] != "" {
		t.Errorf("Users languages = %v, want nb and \"\"", got)
	}
	found, err := dir.SearchUsers(ctx, "Language", 10)
	if err != nil || len(found) != 2 || found[0].PreferredLanguage != "" || found[1].PreferredLanguage != "nb" {
		t.Errorf("SearchUsers = %+v, %v, want None with \"\" then Norsk with nb", found, err)
	}
}

// TestUserDirectory_NotifyHoldsActiveAsHoldersDo proves Notify's "active" is
// PermissionHolders' own: under SCIM a directory-deactivated non-Owner is not
// mailed and a deactivated Owner is, and without SCIM both are.
func TestUserDirectory_NotifyHoldsActiveAsHoldersDo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts []harnessOption
		want []string
	}{
		{"under SCIM", []harnessOption{withEnv("SCIM_TOKEN", "identity-harness-scim-token")}, []string{"f-scim-inactive-owner@example.test"}},
		{"without SCIM", nil, []string{"f-scim-inactive-owner@example.test", "g-scim-inactive@example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newHoldersFixture(t, tc.opts...)
			dir := newUserDirectory(t, f.h)
			for _, name := range []string{"f-scim-inactive-owner", "g-scim-inactive"} {
				if err := dir.Notify(context.Background(), f.users[name], bilingual); err != nil {
					t.Fatalf("Notify(%s): %v", name, err)
				}
			}
			var got []string
			for _, m := range f.h.mail.Messages() {
				got = append(got, m.To)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("mailed = %v, want %v", got, tc.want)
			}
		})
	}
}

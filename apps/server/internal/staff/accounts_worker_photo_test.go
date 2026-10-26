package staff_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The accounts worker and the picture (photo sync design §4): on every
// claim of the identity link the linked user's managed avatar is reconciled
// with HR's photo by its stamp; on the provider link the effective picture
// is pushed to the directory when its stamp is not the one last pushed.

// linkedEmployee is a local employee whose account the worker prepared and
// linked, and the user.
func linkedEmployee(t *testing.T, w accountsWorld, given string) (gen.StaffPerson, uuid.UUID) {
	t.Helper()
	p := w.employee(t, given, "2026-09-01", nil)
	w.cycle(t)
	l := mustLink(t, w.h, p.Id)
	user, err := uuid.Parse(l.User)
	if err != nil {
		t.Fatalf("the link = %+v, want a user", l)
	}
	return p, user
}

// The reconciliation: a photo set while identity could not take it is the
// user's managed avatar by the next claim, stamped as the inline write
// stamps it, and set no more while the stamp holds; a photo removed while
// identity could not clear it is cleared by the next claim. The user's own
// avatar is never touched, and a failing identity leaves the link as it was.
func TestAccountsWorker_TheManagedAvatarIsReconciled(t *testing.T) {
	t.Parallel()
	avatars := modtest.NewFakeAvatars()
	w := newAccountsWorld(t, modtest.WithUsers(avatars.Wrap))
	fake := w.fake
	fake.mirrorAvatars(avatars)
	p, user := linkedEmployee(t, w, "Bilde")
	day := func() { w.h.Advance(24 * time.Hour); w.cycle(t) }

	avatars.SetOwn(user, "image/jpeg", testJPEG(t))
	day()
	if calls := fake.managedAvatarCalls(); len(calls) != 0 {
		t.Errorf("no photo, the user's own avatar: SetManagedAvatar calls %+v, want none", calls)
	}

	fake.failWith("SetManagedAvatar", errors.New("identity is down"))
	set := setPhoto(t, w.hr, p.Id, testPNG(t))
	day()
	if l := mustLink(t, w.h, p.Id); l.LastError != "" || l.Attempts != 0 {
		t.Errorf("the link after a failed reconciliation = %+v, want no failure recorded", l)
	}
	fake.failWith("SetManagedAvatar", nil)
	day()
	stamp := set.PhotoUpdatedAt.UTC().Format(time.RFC3339Nano)
	calls := fake.managedAvatarCalls()
	if len(calls) != 3 || calls[2].avatar == nil || calls[2].avatar.Stamp != stamp || calls[2].avatar.Source != "staff" ||
		calls[2].actor.System != contracts.SystemActorStaffAccounts || !bytes.Equal(calls[2].avatar.Data, testPNG(t)) {
		t.Fatalf("SetManagedAvatar calls = %+v, want the inline set and the worker's failed try, then the worker's set stamped %q", calls, stamp)
	}
	if entry, _ := avatars.Avatar(context.Background(), user); entry == nil || entry.Stamp != "managed:"+stamp {
		t.Errorf("the user's avatar = %+v, want the managed one", entry)
	}
	day()
	if n := len(fake.managedAvatarCalls()); n != 3 {
		t.Errorf("SetManagedAvatar calls with the stamp holding = %d, want 3", n)
	}

	fake.failWith("SetManagedAvatar", errors.New("identity is down"))
	do(t, w.hr, http.MethodDelete, photoPath(p.Id), nil, http.StatusNoContent, nil)
	fake.failWith("SetManagedAvatar", nil)
	day()
	calls = fake.managedAvatarCalls()
	if last := calls[len(calls)-1]; len(calls) != 5 || last.avatar != nil {
		t.Fatalf("SetManagedAvatar calls after the remove = %+v, want the failed inline clear, then the worker's clear", calls)
	}
	if entry, _ := avatars.Avatar(context.Background(), user); entry == nil || entry.ManagedBy != "" {
		t.Errorf("the user's avatar after the clear = %+v, want their own", entry)
	}
	day()
	if n := len(fake.managedAvatarCalls()); n != 5 {
		t.Errorf("SetManagedAvatar calls with nothing to clear = %d, want 5", n)
	}
}

// photoPushWorld is a providerWorld whose directory's avatars are a fake.
type photoPushWorld struct {
	providerWorld
	avatars *modtest.FakeAvatars
}

func newPhotoPushWorld(t *testing.T) photoPushWorld {
	t.Helper()
	avatars := modtest.NewFakeAvatars()
	w := newProviderWorld(t, modtest.WithUsers(avatars.Wrap))
	w.fake.mirrorAvatars(avatars)
	return photoPushWorld{providerWorld: w, avatars: avatars}
}

// arrived is an entra employee whose external account is enabled and whose
// user has arrived in identity and is linked — no push made yet — and the
// user.
func (w photoPushWorld) arrived(t *testing.T, given string) (gen.StaffPerson, uuid.UUID, string) {
	t.Helper()
	p := w.entraEmployee(t, given, "2026-09-01", nil)
	w.cycle(t)
	user := uuid.New()
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive, Managed: true,
		EmployeeNumber: p.EmployeeNumber, HasCredential: true})
	w.h.Advance(time.Hour + time.Minute)
	w.cycle(t)
	if l := mustLink(t, w.h, p.Id); l.User != user.String() {
		t.Fatalf("the identity link = %+v, want the user", l)
	}
	acc, ok := w.prov.ByNumber(p.EmployeeNumber)
	if !ok || acc.State != "enabled" {
		t.Fatalf("the external account = %+v (%v), want enabled", acc, ok)
	}
	return p, user, acc.ExternalID
}

// pushedStamp is the provider link's photo_pushed_stamp, "" for none.
func pushedStamp(t *testing.T, w photoPushWorld, personID int64) string {
	t.Helper()
	return modtest.One[string](t, w.h.Harness,
		`SELECT coalesce(photo_pushed_stamp, '') FROM staff.identity_links WHERE person_id = $1 AND provider = 'entra'`, personID)
}

// photoCodes is the codes of personID's photo pushes, in order.
func photoCodes(t *testing.T, w photoPushWorld, personID int64) []string {
	t.Helper()
	rows, err := w.h.Pool().Query(context.Background(), `
		SELECT payload->>'code' FROM staff.events
		WHERE person_id = $1 AND kind = 'account_external_updated' AND payload->>'step' = 'photo' ORDER BY id`, personID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// photoCalls is how many of the fake provider's calls so far are method.
func photoCalls(w photoPushWorld, method string) int {
	n := 0
	for _, c := range w.prov.Calls() {
		if c.Method == method {
			n++
		}
	}
	return n
}

// The push: HR's photo, the user's managed avatar, is written to the
// enabled account once, its stamp on the link and photo_pushed on the event;
// nothing again while the stamp holds; HR's photo removed, the user's own
// avatar is pushed in its place, and pushed again when it changes; the
// picture gone altogether, the directory's photo is cleared, the stamp
// "none" and photo_cleared on the event.
func TestAccountsWorker_ThePictureIsPushedToTheDirectory(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, externalID := w.arrived(t, "Foto")
	setPhoto(t, w.hr, p.Id, testPNG(t))
	entry, _ := w.avatars.Avatar(context.Background(), user)
	if entry == nil || entry.ManagedBy != "staff" {
		t.Fatalf("the user's avatar = %+v, want HR's photo, managed", entry)
	}

	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto"); n != 1 {
		t.Errorf("SetPhoto calls = %d, want 1", n)
	}
	if got, ok := w.prov.Photo(externalID); !ok || got.ContentType != "image/png" || !bytes.Equal(got.Data, entry.Data) {
		t.Errorf("the directory's photo = %s, %d bytes (%v), want HR's PNG", got.ContentType, len(got.Data), ok)
	}
	if got := pushedStamp(t, w, p.Id); got != entry.Stamp {
		t.Errorf("photo_pushed_stamp = %q, want %q", got, entry.Stamp)
	}
	if got := photoCodes(t, w, p.Id); !slices.Equal(got, []string{"photo_pushed"}) {
		t.Errorf("the push events' codes = %v, want [photo_pushed]", got)
	}

	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto"); n != 1 {
		t.Errorf("SetPhoto calls with the stamp holding = %d, want 1", n)
	}

	own := testJPEG(t)
	w.avatars.SetOwn(user, "image/jpeg", own)
	do(t, w.hr, http.MethodDelete, photoPath(p.Id), nil, http.StatusNoContent, nil)
	w.nextDay()
	w.cycle(t)
	if got, ok := w.prov.Photo(externalID); !ok || got.ContentType != "image/jpeg" || !bytes.Equal(got.Data, own) {
		t.Errorf("the directory's photo after HR's removal = %s (%v), want the user's own JPEG", got.ContentType, ok)
	}
	if got, want := pushedStamp(t, w, p.Id), w.avatars.OwnStamp(user); got != want {
		t.Errorf("photo_pushed_stamp = %q, want %q", got, want)
	}
	w.avatars.SetOwn(user, "image/png", testPNG(t))
	w.nextDay()
	w.cycle(t)
	if got, want := pushedStamp(t, w, p.Id), w.avatars.OwnStamp(user); got != want || photoCalls(w, "SetPhoto") != 3 {
		t.Errorf("after the own avatar changed: stamp %q, SetPhoto calls %d; want %q and 3", got, photoCalls(w, "SetPhoto"), want)
	}

	w.avatars.SetOwn(user, "", nil)
	w.nextDay()
	w.cycle(t)
	if _, ok := w.prov.Photo(externalID); ok || photoCalls(w, "ClearPhoto") != 1 {
		t.Errorf("the picture gone: the directory holds a photo %v, ClearPhoto calls %d; want none and 1", ok, photoCalls(w, "ClearPhoto"))
	}
	if got := pushedStamp(t, w, p.Id); got != "none" {
		t.Errorf("photo_pushed_stamp = %q, want none", got)
	}
	if got := photoCodes(t, w, p.Id); !slices.Equal(got, []string{"photo_pushed", "photo_pushed", "photo_pushed", "photo_cleared"}) {
		t.Errorf("the push events' codes = %v, want three pushes and a clear", got)
	}
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "ClearPhoto"); n != 1 {
		t.Errorf("ClearPhoto calls with none pushed = %d, want 1", n)
	}
}

// A directory that refuses the photo is the account's reported
// photo_not_written at once — the item photoNotWritten for HR — with the
// stamp recorded, so the same picture is not pushed again; a new picture the
// directory takes lifts it.
func TestAccountsWorker_ARefusedPhotoIsReported(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, _ := w.arrived(t, "Avvist")
	w.prov.RefusePhoto(p.EmployeeNumber, true)
	w.avatars.SetOwn(user, "image/png", testPNG(t))
	w.nextDay()
	w.cycle(t)
	if l := mustExtLink(t, w.providerWorld, p.Id); !slices.Contains(l.Codes, contracts.ReportedPhotoNotWritten) || l.LastError != "" {
		t.Errorf("the provider link = %+v, want photo_not_written reported and no failure", l)
	}
	if got, want := pushedStamp(t, w, p.Id), w.avatars.OwnStamp(user); got != want {
		t.Errorf("photo_pushed_stamp = %q, want %q", got, want)
	}
	if got := photoCodes(t, w, p.Id); !slices.Equal(got, []string{"photo_refused"}) {
		t.Errorf("the push events' codes = %v, want [photo_refused]", got)
	}
	items := attentionItems(t, w.hr)
	if !slices.ContainsFunc(items, func(i gen.StaffStatsAttentionItem) bool {
		return i.Type == gen.PhotoNotWritten && i.EntityId == strconv.FormatInt(p.Id, 10) && i.Title == p.DisplayName
	}) {
		t.Errorf("attention = %+v, want photoNotWritten for the person", items)
	}
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto"); n != 1 {
		t.Errorf("SetPhoto calls after the refusal = %d, want 1 (the stamp holds)", n)
	}

	w.prov.RefusePhoto(p.EmployeeNumber, false)
	w.avatars.SetOwn(user, "image/jpeg", testJPEG(t))
	w.nextDay()
	w.cycle(t)
	if l := mustExtLink(t, w.providerWorld, p.Id); slices.Contains(l.Codes, contracts.ReportedPhotoNotWritten) {
		t.Errorf("the provider link's codes after a taken push = %v, want photo_not_written lifted", l.Codes)
	}
	if got := photoCodes(t, w, p.Id); !slices.Equal(got, []string{"photo_refused", "photo_pushed"}) {
		t.Errorf("the push events' codes = %v, want a refusal then a push", got)
	}
}

// A clear the directory refuses is no failure either: the stamp "none" is
// recorded, the account reports photo_not_written, and the event says
// photo_refused.
func TestAccountsWorker_ARefusedClearIsReported(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, _ := w.arrived(t, "Nekta")
	w.avatars.SetOwn(user, "image/png", testPNG(t))
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto"); n != 1 {
		t.Fatalf("SetPhoto calls = %d, want 1", n)
	}

	w.prov.RefusePhoto(p.EmployeeNumber, true)
	w.avatars.SetOwn(user, "", nil)
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "ClearPhoto"); n != 1 {
		t.Errorf("ClearPhoto calls = %d, want 1", n)
	}
	if l := mustExtLink(t, w.providerWorld, p.Id); !slices.Contains(l.Codes, contracts.ReportedPhotoNotWritten) || l.LastError != "" {
		t.Errorf("the provider link = %+v, want photo_not_written reported and no failure", l)
	}
	if got := pushedStamp(t, w, p.Id); got != "none" {
		t.Errorf("photo_pushed_stamp = %q, want none", got)
	}
	if got := photoCodes(t, w, p.Id); !slices.Equal(got, []string{"photo_pushed", "photo_refused"}) {
		t.Errorf("the push events' codes = %v, want a push then a refusal", got)
	}
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "ClearPhoto"); n != 1 {
		t.Errorf("ClearPhoto calls after the refusal = %d, want 1 (the stamp holds)", n)
	}
}

// The first claim of an enabled account with no picture at all removes
// nothing — Vantigo wrote nothing to the directory, so the photo the tenant
// already had stays — and records "none"; a picture later is pushed, and
// once it is gone again, cleared.
func TestAccountsWorker_TheFirstPushOfNothingRemovesNothing(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, externalID := w.arrived(t, "Tom")
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto") + photoCalls(w, "ClearPhoto"); n != 0 {
		t.Errorf("photo calls with no picture ever = %d, want none", n)
	}
	if got := pushedStamp(t, w, p.Id); got != "none" {
		t.Errorf("photo_pushed_stamp = %q, want none", got)
	}
	if got := photoCodes(t, w, p.Id); len(got) != 0 {
		t.Errorf("the push events' codes = %v, want none", got)
	}

	w.avatars.SetOwn(user, "image/png", testPNG(t))
	w.nextDay()
	w.cycle(t)
	if _, ok := w.prov.Photo(externalID); !ok || photoCalls(w, "SetPhoto") != 1 {
		t.Errorf("the own avatar: the directory holds a photo %v, SetPhoto calls %d; want one and 1", ok, photoCalls(w, "SetPhoto"))
	}

	w.avatars.SetOwn(user, "", nil)
	w.nextDay()
	w.cycle(t)
	if _, ok := w.prov.Photo(externalID); ok || photoCalls(w, "ClearPhoto") != 1 {
		t.Errorf("the avatar removed: the directory holds a photo %v, ClearPhoto calls %d; want none and 1", ok, photoCalls(w, "ClearPhoto"))
	}
	if got := pushedStamp(t, w, p.Id); got != "none" {
		t.Errorf("photo_pushed_stamp = %q, want none", got)
	}
}

// A push that fails is the provider link's failure — last_error set, the
// attempts counting on, accountFailing for HR at the third — and is tried
// again at the next claim, which clears it once the directory answers.
func TestAccountsWorker_AFailedPushIsRetried(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, externalID := w.arrived(t, "Feil")
	w.avatars.SetOwn(user, "image/png", testPNG(t))
	w.prov.FailWith("SetPhoto", errors.New("the directory timed out"))
	for want := 1; want <= 3; want++ {
		w.nextDay()
		w.cycle(t)
		var attempts int
		if err := w.h.Pool().QueryRow(context.Background(),
			`SELECT attempts FROM staff.identity_links WHERE person_id = $1 AND provider = 'entra'`, p.Id).Scan(&attempts); err != nil {
			t.Fatal(err)
		}
		if l := mustExtLink(t, w.providerWorld, p.Id); l.LastError == "" || attempts != want || pushedStamp(t, w, p.Id) != "" {
			t.Errorf("after failed push %d: the link = %+v, attempts %d, stamp %q; want last_error, %d attempts, no stamp",
				want, l, attempts, pushedStamp(t, w, p.Id), want)
		}
	}
	failing := func() bool {
		return slices.ContainsFunc(attentionItems(t, w.hr), func(i gen.StaffStatsAttentionItem) bool {
			return i.Type == gen.AccountFailing && i.EntityId == strconv.FormatInt(p.Id, 10)
		})
	}
	if !failing() {
		t.Error("three failed pushes: no accountFailing for the person")
	}
	w.prov.FailWith("SetPhoto", nil)
	w.nextDay()
	w.cycle(t)
	if l, want := mustExtLink(t, w.providerWorld, p.Id), w.avatars.OwnStamp(user); l.LastError != "" || pushedStamp(t, w, p.Id) != want {
		t.Errorf("after the directory answered: the link = %+v, stamp %q; want no failure, %q", l, pushedStamp(t, w, p.Id), want)
	}
	if failing() {
		t.Error("accountFailing stands after the push succeeded")
	}
	if _, ok := w.prov.Photo(externalID); !ok {
		t.Error("the directory holds no photo after the retry")
	}
}

// Nothing is pushed to an account that is not enabled, nor while the
// provider is not connected.
func TestAccountsWorker_NoPushWithoutAnEnabledConnectedAccount(t *testing.T) {
	t.Parallel()
	w := newPhotoPushWorld(t)
	p, user, _ := w.arrived(t, "Stengt")
	w.avatars.SetOwn(user, "image/png", testPNG(t))

	w.prov.SetStatus(contracts.ProviderFailing, "consent_withdrawn")
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto") + photoCalls(w, "ClearPhoto"); n != 0 {
		t.Errorf("photo calls with the provider not connected = %d, want none", n)
	}

	w.prov.SetStatus(contracts.ProviderConnected, "")
	w.h.Exec(t, `UPDATE staff.people SET provision_account = false WHERE id = $1`, p.Id)
	w.nextDay()
	w.cycle(t)
	w.nextDay()
	w.cycle(t)
	if n := photoCalls(w, "SetPhoto") + photoCalls(w, "ClearPhoto"); n != 0 {
		t.Errorf("photo calls with the flag off = %d, want none", n)
	}
	if got := pushedStamp(t, w, p.Id); got != "" {
		t.Errorf("photo_pushed_stamp = %q, want none pushed", got)
	}
}

// An Owner's or a SystemAdmin's picture is theirs: a managed avatar Staff
// set before the user became one is cleared by the next claim, and none is
// set again while they are one.
func TestAccountsWorker_APrivilegedUsersManagedAvatarIsCleared(t *testing.T) {
	t.Parallel()
	avatars := modtest.NewFakeAvatars()
	w := newAccountsWorld(t, modtest.WithUsers(avatars.Wrap))
	w.fake.mirrorAvatars(avatars)
	p, user := linkedEmployee(t, w, "Sjef")
	day := func() { w.h.Advance(24 * time.Hour); w.cycle(t) }
	setPhoto(t, w.hr, p.Id, testPNG(t))
	if entry, _ := avatars.Avatar(context.Background(), user); entry == nil || entry.ManagedBy != "staff" {
		t.Fatalf("the user's avatar = %+v, want HR's photo, managed", entry)
	}

	ref := w.fake.user(user)
	ref.Privileged = true
	w.fake.setUser(ref)
	day()
	calls := w.fake.managedAvatarCalls()
	if last := calls[len(calls)-1]; len(calls) != 2 || last.avatar != nil || last.user != user {
		t.Fatalf("SetManagedAvatar calls = %+v, want the inline set, then the worker's clear", calls)
	}
	if entry, _ := avatars.Avatar(context.Background(), user); entry != nil {
		t.Errorf("the privileged user's avatar = %+v, want none", entry)
	}
	day()
	if n := len(w.fake.managedAvatarCalls()); n != 2 {
		t.Errorf("SetManagedAvatar calls with nothing to clear = %d, want 2", n)
	}
}

// Two HR sets whose inline writes reach identity out of order — the first
// set's landing last, as two concurrent PUTs may — leave the older photo as
// the managed avatar for a moment only: its stamp is not the person's, so
// the next claim sets the photo that stands.
func TestAccountsWorker_AnOutOfOrderInlineWriteIsHealedByTheStamp(t *testing.T) {
	t.Parallel()
	avatars := modtest.NewFakeAvatars()
	w := newAccountsWorld(t, modtest.WithUsers(avatars.Wrap))
	w.fake.mirrorAvatars(avatars)
	p, user := linkedEmployee(t, w, "Kappløp")
	first := setPhoto(t, w.hr, p.Id, testPNG(t))
	w.h.Advance(time.Minute)
	second := setPhoto(t, w.hr, p.Id, testJPEG(t))
	calls := w.fake.managedAvatarCalls()
	if len(calls) != 2 {
		t.Fatalf("SetManagedAvatar calls = %+v, want the two inline sets", calls)
	}
	// The first request's write lands after the second's.
	avatars.SetManaged(user, calls[0].avatar)
	if entry, _ := avatars.Avatar(context.Background(), user); entry == nil || entry.Stamp != "managed:"+first.PhotoUpdatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("the user's avatar = %+v, want the first photo standing", entry)
	}

	w.h.Advance(24 * time.Hour)
	w.cycle(t)
	want := "managed:" + second.PhotoUpdatedAt.UTC().Format(time.RFC3339Nano)
	if entry, _ := avatars.Avatar(context.Background(), user); entry == nil || entry.Stamp != want || !bytes.Equal(entry.Data, testJPEG(t)) {
		t.Errorf("the user's avatar after the claim = %+v, want the second photo stamped %q", entry, want)
	}
}

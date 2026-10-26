package staff_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The photo follows the account (photo sync design §4): HR's photo made the
// linked user's managed avatar once its write commits, and the effective
// picture — HR's, else the linked user's own avatar — on the Staff page.

// photoSyncWorld is an absenceWorld whose accounts are the fake, holding
// Kari's and Bjørn's users, and whose directory answers avatars from a fake
// the accounts' managed writes reach.
type photoSyncWorld struct {
	absenceWorld
	fake    *fakeAccounts
	avatars *modtest.FakeAvatars
}

func newPhotoSyncWorld(t *testing.T) photoSyncWorld {
	t.Helper()
	fake := newFakeAccounts()
	avatars := modtest.NewFakeAvatars()
	fake.mirrorAvatars(avatars)
	w := newAbsenceWorld(t, withAccounts(fake), modtest.WithUsers(avatars.Wrap))
	for _, user := range []uuid.UUID{w.kariUser, w.bossUser} {
		fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive})
	}
	return photoSyncWorld{absenceWorld: w, fake: fake, avatars: avatars}
}

// avatarOf is the user's effective avatar as the directory answers it.
func (w photoSyncWorld) avatarOf(t *testing.T, user uuid.UUID) *contracts.AvatarEntry {
	t.Helper()
	entry, err := w.avatars.Avatar(context.Background(), user)
	if err != nil {
		t.Fatalf("read user %s's avatar: %v", user, err)
	}
	return entry
}

// HR's set makes the stored photo — the stripped bytes, its type — the
// linked user's managed avatar at once, from Staff, stamped with
// photoUpdatedAt in RFC 3339 with its fraction, for HR as the actor; the
// remove clears it, and the user's own avatar shows again.
func TestPhotoSync_HRsPhotoBecomesTheAccountsPicture(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	w.avatars.SetOwn(w.kariUser, "image/jpeg", testJPEG(t))

	set := setPhoto(t, w.hr, w.kari.Id, testPNG(t))
	stored := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil)
	calls := w.fake.managedAvatarCalls()
	if len(calls) != 1 || calls[0].avatar == nil {
		t.Fatalf("SetManagedAvatar calls = %+v, want one set", calls)
	}
	stamp := set.PhotoUpdatedAt.UTC().Format(time.RFC3339Nano)
	got := calls[0]
	if got.user != w.kariUser || got.avatar.ContentType != "image/png" || !bytes.Equal(got.avatar.Data, stored.Body) ||
		got.avatar.Source != "staff" || got.avatar.Stamp != stamp || got.actor.UserID == nil || got.actor.System != "" {
		t.Errorf("the set = user %s, %s, %d bytes (stored %d), source %q, stamp %q, actor %+v; want Kari's user, image/png, the stored bytes, staff, %q, HR",
			got.user, got.avatar.ContentType, len(got.avatar.Data), len(stored.Body), got.avatar.Source, got.avatar.Stamp, got.actor, stamp)
	}
	if set.PhotoSource == nil || *set.PhotoSource != gen.PhotoFromRecord {
		t.Errorf("photoSource = %v, want record", set.PhotoSource)
	}
	if entry := w.avatarOf(t, w.kariUser); entry == nil || entry.Stamp != "managed:"+stamp {
		t.Errorf("Kari's avatar = %+v, want the managed one stamped %q", entry, stamp)
	}

	do(t, w.hr, http.MethodDelete, photoPath(w.kari.Id), nil, http.StatusNoContent, nil)
	calls = w.fake.managedAvatarCalls()
	if len(calls) != 2 || calls[1].user != w.kariUser || calls[1].avatar != nil {
		t.Fatalf("SetManagedAvatar calls after the remove = %+v, want a clear for Kari's user", calls)
	}
	if entry := w.avatarOf(t, w.kariUser); entry == nil || entry.ManagedBy != "" {
		t.Errorf("Kari's avatar after the remove = %+v, want her own", entry)
	}
}

// A person linked to nobody has no account to give the picture: nothing is
// called. A failing identity, and an Owner's or a SystemAdmin's account
// (whose picture is theirs), are logged — the set is 200 and the photo
// stands, for the worker to put right.
func TestPhotoSync_TheRequestNeverFailsOnTheAccount(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)

	loner, _ := employed(t, w.hr, "Ensom", "2020-01-01", terms())
	setPhoto(t, w.hr, loner.Id, testPNG(t))
	do(t, w.hr, http.MethodDelete, photoPath(loner.Id), nil, http.StatusNoContent, nil)
	if calls := w.fake.managedAvatarCalls(); len(calls) != 0 {
		t.Errorf("SetManagedAvatar calls for a person with no link = %+v, want none", calls)
	}

	w.fake.failWith("SetManagedAvatar", errors.New("identity is down"))
	data := testPNG(t)
	if got := setPhoto(t, w.hr, w.kari.Id, data); got.PhotoUpdatedAt == nil {
		t.Error("the set with identity down: no photoUpdatedAt, want the photo")
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusOK || !bytes.Equal(r.Body, data) {
		t.Errorf("the photo with identity down: status %d, want 200 with HR's bytes", r.Status)
	}
	if n := len(w.fake.managedAvatarCalls()); n != 1 {
		t.Errorf("SetManagedAvatar calls = %d, want the one that failed", n)
	}
	w.fake.failWith("SetManagedAvatar", nil)

	w.fake.setUser(contracts.AccountRef{UserID: w.bossUser, State: contracts.AccountStateActive, Privileged: true})
	setPhoto(t, w.hr, w.boss.Id, testPNG(t))
	do(t, w.hr, http.MethodDelete, photoPath(w.boss.Id), nil, http.StatusNoContent, nil)
	if entry := w.avatarOf(t, w.bossUser); entry != nil {
		t.Errorf("the Owner's avatar = %+v, want none (their picture is theirs)", entry)
	}
}

// While HR has set no photo, the person's picture is the linked user's own
// avatar: the person and My page say account with the avatar's time, and
// GET /photo streams its bytes with the photo's headers; HR's photo wins
// (record) once set. Neither is no picture, and a directory that cannot
// answer is no picture too, never an error. The directory's list row keeps
// Staff's own photoUpdatedAt (reading 9).
func TestPhotoSync_TheEffectivePicture(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	own := testJPEG(t)
	w.avatars.SetOwn(w.kariUser, "image/jpeg", own)
	entry := w.avatarOf(t, w.kariUser)

	var asViewer, mine gen.StaffPerson
	do(t, w.viewer, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &asViewer)
	do(t, w.kariC, http.MethodGet, mePath, nil, http.StatusOK, &mine)
	for name, got := range map[string]gen.StaffPerson{"a colleague": asViewer, "My page": mine} {
		if got.PhotoSource == nil || *got.PhotoSource != gen.PhotoFromAccount || got.PhotoUpdatedAt == nil || !got.PhotoUpdatedAt.Equal(entry.UpdatedAt) {
			t.Errorf("%s: photoSource %v photoUpdatedAt %v, want account at %v", name, got.PhotoSource, got.PhotoUpdatedAt, entry.UpdatedAt)
		}
	}
	r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil)
	if r.Status != http.StatusOK || !bytes.Equal(r.Body, own) || r.Header("Content-Type") != "image/jpeg" ||
		r.Header("Content-Length") != strconv.Itoa(len(own)) || r.Header("Cache-Control") != "private, max-age=300" ||
		r.Header("X-Content-Type-Options") != "nosniff" {
		t.Errorf("GET the photo: status %d type %q length %q, want 200 with the user's own avatar and the photo's headers",
			r.Status, r.Header("Content-Type"), r.Header("Content-Length"))
	}
	list := listPeople(t, w.hr, "?q=Kari")
	for _, row := range list.Data {
		if row.Id == w.kari.Id && row.PhotoUpdatedAt != nil {
			t.Errorf("the list row's photoUpdatedAt = %v, want Staff's own (none)", row.PhotoUpdatedAt)
		}
	}

	hrs := testPNG(t)
	set := setPhoto(t, w.hr, w.kari.Id, hrs)
	do(t, w.viewer, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &asViewer)
	if asViewer.PhotoSource == nil || *asViewer.PhotoSource != gen.PhotoFromRecord || !asViewer.PhotoUpdatedAt.Equal(*set.PhotoUpdatedAt) {
		t.Errorf("with HR's photo: photoSource %v photoUpdatedAt %v, want record at %v", asViewer.PhotoSource, asViewer.PhotoUpdatedAt, set.PhotoUpdatedAt)
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusOK || !bytes.Equal(r.Body, hrs) || r.Header("Content-Type") != "image/png" {
		t.Errorf("GET the photo with HR's: status %d type %q, want 200 with HR's PNG", r.Status, r.Header("Content-Type"))
	}

	var boss gen.StaffPerson
	do(t, w.hr, http.MethodGet, personPath(w.boss.Id), nil, http.StatusOK, &boss)
	if boss.PhotoSource != nil || boss.PhotoUpdatedAt != nil {
		t.Errorf("neither picture: photoSource %v photoUpdatedAt %v, want both absent", boss.PhotoSource, boss.PhotoUpdatedAt)
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.boss.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("GET the photo with neither: status %d body %q, want the bare 404", r.Status, r.Body)
	}

	do(t, w.hr, http.MethodDelete, photoPath(w.kari.Id), nil, http.StatusNoContent, nil)
	w.avatars.FailWith(errors.New("identity is down"))
	var down gen.StaffPerson
	do(t, w.viewer, http.MethodGet, personPath(w.kari.Id), nil, http.StatusOK, &down)
	if down.PhotoSource != nil || down.PhotoUpdatedAt != nil {
		t.Errorf("the directory down: photoSource %v photoUpdatedAt %v, want no picture", down.PhotoSource, down.PhotoUpdatedAt)
	}
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("GET the photo with the directory down: status %d, want 404", r.Status)
	}
}

// The picture follows the self-service link: relinked from one user to
// another, the first loses HR's photo and the second is given it; unlinked,
// the user loses it; a link made after HR set the photo gives it at once.
// Each change makes the identity link due.
func TestPhotoSync_ThePictureFollowsTheLink(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	_, other := signIn(t, w.h)
	w.fake.setUser(contracts.AccountRef{UserID: other, State: contracts.AccountStateActive})
	set := setPhoto(t, w.hr, w.kari.Id, testPNG(t))
	stamp := "managed:" + set.PhotoUpdatedAt.UTC().Format(time.RFC3339Nano)

	linkThroughAPI(t, w.hr, w.kari.Id, other)
	if entry := w.avatarOf(t, w.kariUser); entry != nil {
		t.Errorf("the old user's avatar after the relink = %+v, want none", entry)
	}
	if entry := w.avatarOf(t, other); entry == nil || entry.Stamp != stamp {
		t.Errorf("the new user's avatar after the relink = %+v, want HR's photo stamped %q", entry, stamp)
	}

	do(t, w.hr, http.MethodPut, userPath(w.kari.Id), map[string]any{"userId": nil}, http.StatusOK, nil)
	if entry := w.avatarOf(t, other); entry != nil {
		t.Errorf("the user's avatar after the unlink = %+v, want none", entry)
	}

	linkThroughAPI(t, w.hr, w.kari.Id, w.kariUser)
	if entry := w.avatarOf(t, w.kariUser); entry == nil || entry.Stamp != stamp {
		t.Errorf("the user's avatar after a link made after the photo = %+v, want HR's photo", entry)
	}
	calls := w.fake.managedAvatarCalls()
	if len(calls) != 5 {
		t.Errorf("SetManagedAvatar calls = %+v, want the set, the relink's clear and set, the unlink's clear and the link's set", calls)
	}

	// A link to the user already linked changes nothing.
	linkThroughAPI(t, w.hr, w.kari.Id, w.kariUser)
	if n := len(w.fake.managedAvatarCalls()); n != 5 {
		t.Errorf("SetManagedAvatar calls after linking the same user = %d, want 5", n)
	}
}

// HR's set and remove make the person's identity link due at once, so the
// worker's next poll — not midnight — pushes the picture on.
func TestPhotoSync_ThePhotosWriteMakesTheLinkDue(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	due := func() bool {
		return modtest.One[bool](t, w.h.Harness, `SELECT next_attempt_at <= $2 FROM staff.identity_links
			WHERE person_id = $1 AND provider = 'identity'`, w.kari.Id, w.h.Now())
	}
	later := w.h.Now().Add(20 * time.Hour)
	w.h.Exec(t, `UPDATE staff.identity_links SET next_attempt_at = $2 WHERE person_id = $1 AND provider = 'identity'`, w.kari.Id, later)
	setPhoto(t, w.hr, w.kari.Id, testPNG(t))
	if !due() {
		t.Error("after HR's set: the identity link is not due")
	}
	w.h.Exec(t, `UPDATE staff.identity_links SET next_attempt_at = $2 WHERE person_id = $1 AND provider = 'identity'`, w.kari.Id, later)
	do(t, w.hr, http.MethodDelete, photoPath(w.kari.Id), nil, http.StatusNoContent, nil)
	if !due() {
		t.Error("after HR's remove: the identity link is not due")
	}
}

// A caller who does not see the person is not shown the linked user's own
// avatar either: the bare 404.
func TestPhotoSync_TheFallbackIsRefusedToWhoDoesNotSee(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	w.avatars.SetOwn(w.kariUser, "image/jpeg", testJPEG(t))
	if r := w.viewer.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusOK {
		t.Fatalf("GET the photo as a colleague: status %d, want 200 with the user's own avatar", r.Status)
	}
	if r := w.nobody.Do(http.MethodGet, photoPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("GET the photo as someone who does not see Kari: status %d body %d bytes, want the bare 404", r.Status, len(r.Body))
	}
}

// The erase takes the person's photo off their account: the managed avatar
// of the user their link names is cleared by the retention run, and an
// erased person is never shown the linked user's own avatar — no
// photoSource, and GET /photo is the bare 404.
func TestPhotoSync_TheEraseClearsTheAccountsPicture(t *testing.T) {
	t.Parallel()
	w := newPhotoSyncWorld(t)
	leaver := createPerson(t, w.hr, with(newPerson("Gått", "Ut"), "notes", "x"))
	pastEmployment(t, w.hr, leaver.Id, "2020-01-01", "2024-12-31", terms())
	_, user := signIn(t, w.h)
	w.fake.setUser(contracts.AccountRef{UserID: user, State: contracts.AccountStateActive})
	linkThroughAPI(t, w.hr, leaver.Id, user)
	setPhoto(t, w.hr, leaver.Id, testPNG(t))
	w.avatars.SetOwn(user, "image/jpeg", testJPEG(t))
	if entry := w.avatarOf(t, user); entry == nil || entry.ManagedBy != "staff" {
		t.Fatalf("the user's avatar = %+v, want HR's photo, managed", entry)
	}

	// The retention worker as the module contributes it, with the accounts.
	var retention *staff.RetentionWorker
	for _, wk := range w.h.Workers(w.h.Deps()) {
		if rw, ok := wk.(*staff.RetentionWorker); ok {
			retention = rw
		}
	}
	if ran, err := retention.RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	if n := w.h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND erased_at IS NOT NULL`, leaver.Id); n != 1 {
		t.Fatal("the leaver is not erased")
	}
	calls := w.fake.managedAvatarCalls()
	if last := calls[len(calls)-1]; last.user != user || last.avatar != nil || last.actor.System != contracts.SystemActorStaffRetention {
		t.Errorf("the last SetManagedAvatar call = %+v, want the retention run's clear for the user", last)
	}
	if entry := w.avatarOf(t, user); entry == nil || entry.ManagedBy != "" {
		t.Errorf("the user's avatar after the erase = %+v, want their own", entry)
	}

	var erased gen.StaffPerson
	do(t, w.hr, http.MethodGet, personPath(leaver.Id), nil, http.StatusOK, &erased)
	if erased.PhotoSource != nil || erased.PhotoUpdatedAt != nil {
		t.Errorf("the erased person: photoSource %v photoUpdatedAt %v, want neither", erased.PhotoSource, erased.PhotoUpdatedAt)
	}
	if r := w.hr.Do(http.MethodGet, photoPath(leaver.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("GET the erased person's photo: status %d body %d bytes, want the bare 404", r.Status, len(r.Body))
	}
}

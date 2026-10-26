package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The managed avatar (staff photo sync, Task 3a): Staff's photo set through
// AccountProvisioning.SetManagedAvatar stands in front of the user's own
// avatar, which the user cannot change while it stands and which shows
// again once it is cleared.

// managedStamp is Staff's photo_updated_at as SetManagedAvatar is given it.
const managedStamp = "2026-10-25T10:00:00Z"

// staffPhoto is a managed avatar from Staff with data, declared as ct.
func staffPhoto(ct string, data []byte) *contracts.ManagedAvatar {
	return &contracts.ManagedAvatar{ContentType: ct, Data: data, Source: "staff", Stamp: managedStamp}
}

// testGIF encodes a tiny GIF, a real image identity does not admit.
func testGIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black}), nil); err != nil {
		t.Fatalf("encode GIF: %v", err)
	}
	return buf.Bytes()
}

// avatarManagedBy is GET /account's avatarManagedBy, failing t unless the
// key is present (null or a string).
func avatarManagedBy(t *testing.T, c *client) *string {
	t.Helper()
	r := c.do(http.MethodGet, accountPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", accountPath, r.status, r.body)
	}
	var fields map[string]json.RawMessage
	r.json(&fields)
	raw, ok := fields["avatarManagedBy"]
	if !ok {
		t.Fatalf("GET %s answers no avatarManagedBy: %s", accountPath, r.body)
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("avatarManagedBy %s: %v", raw, err)
	}
	return v
}

// mustAvatar is the directory's effective avatar of id.
func mustAvatar(t *testing.T, h *harness, id uuid.UUID) *contracts.AvatarEntry {
	t.Helper()
	a, err := newUserDirectory(t, h).Avatar(context.Background(), id)
	if err != nil {
		t.Fatalf("Avatar(%s): %v", id, err)
	}
	return a
}

// mustAvatarInfo is the directory's AvatarInfo for id; it must agree with
// Avatar's entry, bytes aside.
func mustAvatarInfo(t *testing.T, h *harness, id uuid.UUID) *contracts.AvatarInfo {
	t.Helper()
	info, err := newUserDirectory(t, h).AvatarInfo(context.Background(), id)
	if err != nil {
		t.Fatalf("AvatarInfo(%s): %v", id, err)
	}
	a := mustAvatar(t, h, id)
	switch {
	case a == nil && info != nil, a != nil && info == nil:
		t.Errorf("AvatarInfo(%s) = %+v, Avatar = %+v; want both or neither", id, info, a)
	case a != nil && (info.ContentType != a.ContentType || info.Stamp != a.Stamp || info.ManagedBy != a.ManagedBy || !info.UpdatedAt.Equal(a.UpdatedAt)):
		t.Errorf("AvatarInfo(%s) = %+v, want Avatar's %+v", id, info, a)
	}
	return info
}

// ownStamp is the stamp of a user's own first avatar: its version and the
// unix seconds of its write.
var ownStamp = regexp.MustCompile(`^own:1@[0-9]+$`)

// The Staff system's managed avatar stands in front of the user's own: the
// user's GET serves it with the private headers, GET /account says Staff
// keeps it, the directory answers it with its stamp, and the user's POST,
// PUT and DELETE answer 409 avatar_managed — even an upload past the
// request cap, since the body is never read — leaving the own avatar as it
// is. The same avatar again writes nothing. Cleared, the own avatar shows
// again, byte for byte, and the user may change it once more.
func TestManagedAvatar_StandsInFrontOfTheOwnAndClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "managed@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)
	p := h.accounts(t)

	own := testPNG(t, 4, 4)
	ct, body := avatarUpload(t, "image/png", own)
	if r := c.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusOK {
		t.Fatalf("own upload: status %d body %s", r.status, r.body)
	}
	if by := avatarManagedBy(t, c); by != nil {
		t.Errorf("avatarManagedBy = %q with only the own avatar, want null", *by)
	}

	photo := testJPEG(t, 8, 8)
	if err := p.SetManagedAvatar(context.Background(), systemActor, id, staffPhoto("image/jpeg", photo)); err != nil {
		t.Fatalf("SetManagedAvatar: %v", err)
	}

	get := c.do(http.MethodGet, avatarPath, nil)
	if get.status != http.StatusOK || get.header("Content-Type") != "image/jpeg" || !bytes.Equal(get.body, photo) {
		t.Fatalf("GET %s while managed: status %d Content-Type %q, %d bytes; want Staff's JPEG", avatarPath, get.status, get.header("Content-Type"), len(get.body))
	}
	for k, want := range map[string]string{"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Disposition": "inline"} {
		if got := get.header(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if by := avatarManagedBy(t, c); by == nil || *by != "staff" {
		t.Errorf("avatarManagedBy = %v while managed, want staff", by)
	}
	if acc := getAccount(t, c); acc.AvatarUrl == nil || *acc.AvatarUrl != avatarPath {
		t.Errorf("avatarUrl = %v while managed, want %s", acc.AvatarUrl, avatarPath)
	}
	a := mustAvatar(t, h, id)
	if a == nil || a.Stamp != "managed:"+managedStamp || a.ManagedBy != "staff" || a.ContentType != "image/jpeg" || !bytes.Equal(a.Data, photo) || a.UpdatedAt.IsZero() {
		t.Errorf("Avatar while managed = %+v, want Staff's JPEG stamped managed:%s", a, managedStamp)
	}

	ct, body = avatarUpload(t, "image/png", testPNG(t, 2, 2))
	big, bigBody := avatarUploadWithFiller(t, 5*1024*1024+64*1024+4096, "image/png", testPNG(t, 2, 2))
	for _, op := range []struct {
		method string
		opts   []reqOpt
	}{
		{http.MethodPost, []reqOpt{rawBody(ct, body)}},
		{http.MethodPut, []reqOpt{rawBody(ct, body)}},
		{http.MethodPut, []reqOpt{rawBody(big, bigBody)}},
		{http.MethodDelete, nil},
	} {
		r := c.do(op.method, avatarPath, nil, op.opts...)
		if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "avatar_managed" || e.Message != "Your photo is kept by Staff and cannot be changed here." {
			t.Errorf("%s %s while managed: status %d error %+v, want 409 avatar_managed", op.method, avatarPath, r.status, e)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.profile_avatars WHERE user_id = $1 AND data = $2 AND version = 1`, id, own); n != 1 {
		t.Error("the own avatar changed while managed")
	}

	events := h.auditEvents(t, "user.avatar-managed")
	wantAfter := fmt.Sprintf(`{"source": "staff", "stamp": %q, "contentType": "image/jpeg", "bytes": %d}`, managedStamp, len(photo))
	if len(events) != 1 || events[0].Actor != nil || events[0].Details != `{"action":"user.avatar-managed","actor":"system:staff-accounts"}` ||
		events[0].TargetUser == nil || *events[0].TargetUser != id || events[0].Before != "null" || !sameJSON(t, events[0].After, wantAfter) {
		t.Errorf("audit = %+v, want one user.avatar-managed row by the system with %s", events, wantAfter)
	}

	before := footprint(t, h)
	if err := p.SetManagedAvatar(context.Background(), systemActor, id, staffPhoto("image/jpeg", photo)); err != nil {
		t.Fatalf("SetManagedAvatar again: %v", err)
	}
	if after := footprint(t, h); after != before || len(h.auditEvents(t, "user.avatar-managed")) != 1 {
		t.Errorf("the same avatar again wrote: %s → %s", before, after)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.managed_avatars WHERE user_id = $1 AND version = 1`, id); n != 1 {
		t.Error("the same avatar again bumped the version")
	}

	if err := p.SetManagedAvatar(context.Background(), systemActor, id, nil); err != nil {
		t.Fatalf("SetManagedAvatar(nil): %v", err)
	}
	back := c.do(http.MethodGet, avatarPath, nil)
	if back.status != http.StatusOK || back.header("Content-Type") != "image/png" || !bytes.Equal(back.body, own) {
		t.Fatalf("GET %s after the clear: status %d Content-Type %q; want the own PNG", avatarPath, back.status, back.header("Content-Type"))
	}
	if by := avatarManagedBy(t, c); by != nil {
		t.Errorf("avatarManagedBy = %q after the clear, want null", *by)
	}
	if a := mustAvatar(t, h, id); a == nil || !ownStamp.MatchString(a.Stamp) || a.ManagedBy != "" || a.ContentType != "image/png" || !bytes.Equal(a.Data, own) {
		t.Errorf("Avatar after the clear = %+v, want the own PNG stamped own:1@<seconds>", a)
	}
	if info := mustAvatarInfo(t, h, id); info == nil || info.ManagedBy != "" {
		t.Errorf("AvatarInfo after the clear = %+v, want the own PNG's", info)
	}
	unmanaged := h.auditEvents(t, "user.avatar-unmanaged")
	wantBefore := fmt.Sprintf(`{"source": "staff", "stamp": %q, "contentType": "image/jpeg", "bytes": %d}`, managedStamp, len(photo))
	if len(unmanaged) != 1 || unmanaged[0].Details != `{"action":"user.avatar-unmanaged","actor":"system:staff-accounts"}` ||
		!sameJSON(t, unmanaged[0].Before, wantBefore) || unmanaged[0].After != "null" {
		t.Errorf("audit = %+v, want one user.avatar-unmanaged row naming the removed avatar", unmanaged)
	}

	if r := c.do(http.MethodPost, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusOK {
		t.Errorf("POST %s after the clear: status %d body %s, want 200", avatarPath, r.status, r.body)
	}
	if r := c.do(http.MethodDelete, avatarPath, nil); r.status != http.StatusNoContent {
		t.Errorf("DELETE %s after the clear: status %d, want 204", avatarPath, r.status)
	}
	if a := mustAvatar(t, h, id); a != nil {
		t.Errorf("Avatar with none = %+v, want nil", a)
	}

	// A clear with nothing managed changes nothing and is no error.
	before = footprint(t, h)
	if err := p.SetManagedAvatar(context.Background(), systemActor, id, nil); err != nil {
		t.Errorf("SetManagedAvatar(nil) with none: %v", err)
	}
	if after := footprint(t, h); after != before || len(h.auditEvents(t, "user.avatar-unmanaged")) != 1 {
		t.Errorf("a clear with none wrote: %s → %s", before, after)
	}
}

// A user's actor is written as the audit row's actor; a replace by another
// stamp writes the new one and audits the old as before.
func TestManagedAvatar_AUserActorIsAuditedAndAReplaceRecordsBoth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	id := h.insertUser(t, "person@example.test", identity.RoleUserID)
	p := h.accounts(t)

	first := testPNG(t, 3, 3)
	if err := p.SetManagedAvatar(context.Background(), userActor(ownerID), id, staffPhoto("image/png", first)); err != nil {
		t.Fatalf("SetManagedAvatar: %v", err)
	}
	second := testJPEG(t, 5, 5)
	next := &contracts.ManagedAvatar{ContentType: "image/jpeg", Data: second, Source: "staff", Stamp: "2026-10-26T08:00:00Z"}
	if err := p.SetManagedAvatar(context.Background(), userActor(ownerID), id, next); err != nil {
		t.Fatalf("SetManagedAvatar again: %v", err)
	}
	events := h.auditEvents(t, "user.avatar-managed")
	if len(events) != 2 || events[1].Actor == nil || *events[1].Actor != ownerID || events[1].Details != `{"action":"user.avatar-managed"}` ||
		!sameJSON(t, events[1].Before, fmt.Sprintf(`{"source":"staff","stamp":%q,"contentType":"image/png","bytes":%d}`, managedStamp, len(first))) ||
		!sameJSON(t, events[1].After, fmt.Sprintf(`{"source":"staff","stamp":"2026-10-26T08:00:00Z","contentType":"image/jpeg","bytes":%d}`, len(second))) {
		t.Errorf("audit = %+v, want the replace by the user", events)
	}
	for _, e := range events {
		if strings.Contains(e.Before+e.After, "data") {
			t.Errorf("an audit row carries the bytes: %+v", e)
		}
	}
	if a := mustAvatar(t, h, id); a == nil || a.Stamp != "managed:2026-10-26T08:00:00Z" || !bytes.Equal(a.Data, second) {
		t.Errorf("Avatar = %+v, want the replacement", a)
	}
	if info := mustAvatarInfo(t, h, id); info == nil || info.ManagedBy != "staff" {
		t.Errorf("AvatarInfo = %+v, want the replacement's", info)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.managed_avatars WHERE user_id = $1 AND version = 2`, id); n != 1 {
		t.Error("the replace did not bump the version")
	}
}

// SetManagedAvatar's refusals, each writing nothing: an invalid actor
// (ErrInvalidActor), no such user (ErrUserGone), an Owner or a SystemAdmin
// (ErrAccountPrivileged — a set; a clear is allowed), and an image identity
// does not admit or a source other than Staff (ErrAvatarInvalid).
func TestManagedAvatar_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	admin := h.insertUser(t, "admin@example.test", identity.RoleUserID, identity.RoleSystemAdminID)
	id := h.insertUser(t, "plain@example.test", identity.RoleUserID)
	p := h.accounts(t)
	ctx := context.Background()
	photo := staffPhoto("image/png", testPNG(t, 4, 4))
	before := footprint(t, h)

	nilUser, someone := uuid.Nil, uuid.New()
	for name, actor := range map[string]contracts.Actor{
		"none":            {},
		"the nil user":    {UserID: &nilUser},
		"another system":  {System: "system:other"},
		"user and system": {UserID: &someone, System: contracts.SystemActorStaffAccounts},
	} {
		wantErr(t, name, p.SetManagedAvatar(ctx, actor, id, photo), contracts.ErrInvalidActor)
		wantErr(t, name+", a clear", p.SetManagedAvatar(ctx, actor, id, nil), contracts.ErrInvalidActor)
	}
	wantErr(t, "an unknown user", p.SetManagedAvatar(ctx, systemActor, uuid.New(), photo), contracts.ErrUserGone)
	wantErr(t, "an Owner", p.SetManagedAvatar(ctx, systemActor, ownerID, photo), contracts.ErrAccountPrivileged)
	wantErr(t, "a SystemAdmin", p.SetManagedAvatar(ctx, systemActor, admin, photo), contracts.ErrAccountPrivileged)
	if err := p.SetManagedAvatar(ctx, systemActor, ownerID, nil); err != nil {
		t.Errorf("an Owner's clear with none: %v, want nil", err)
	}

	for name, a := range map[string]*contracts.ManagedAvatar{
		"a GIF":                 staffPhoto("image/gif", testGIF(t)),
		"a PNG declared a JPEG": staffPhoto("image/jpeg", testPNG(t, 4, 4)),
		"4097 px wide":          staffPhoto("image/png", testPNG(t, 4097, 1)),
		"no bytes":              staffPhoto("image/png", nil),
		"over 5 MiB":            staffPhoto("image/png", append(testPNG(t, 4, 4), make([]byte, 5*1024*1024)...)),
		"from another source":   {ContentType: "image/png", Data: testPNG(t, 4, 4), Source: "other", Stamp: managedStamp},
	} {
		wantErr(t, name, p.SetManagedAvatar(ctx, systemActor, id, a), contracts.ErrAvatarInvalid)
	}
	if after := footprint(t, h); after != before {
		t.Errorf("the refusals wrote: %s → %s", before, after)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.managed_avatars`); n != 0 {
		t.Errorf("%d managed avatars after the refusals, want none", n)
	}
}

// A user made a SystemAdmin after Staff set their managed avatar is cleared
// all the same: only a set is refused a privileged user, so their own
// picture shows again.
func TestManagedAvatar_APrivilegedUsersClearIsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "promoted@example.test", identity.RoleUserID)
	p := h.accounts(t)
	ctx := context.Background()
	if err := p.SetManagedAvatar(ctx, systemActor, id, staffPhoto("image/png", testPNG(t, 4, 4))); err != nil {
		t.Fatalf("SetManagedAvatar: %v", err)
	}
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleSystemAdminID)
	if err := p.SetManagedAvatar(ctx, systemActor, id, nil); err != nil {
		t.Fatalf("a SystemAdmin's clear: %v, want nil", err)
	}
	if a := mustAvatar(t, h, id); a != nil {
		t.Errorf("Avatar after the clear = %+v, want none", a)
	}
	if n := len(h.auditEvents(t, "user.avatar-unmanaged")); n != 1 {
		t.Errorf("%d user.avatar-unmanaged rows, want 1", n)
	}
}

// The directory's Avatar is nil for a user with none and for no user.
func TestManagedAvatar_TheDirectoryAnswersNilForNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "bare@example.test", identity.RoleUserID)
	if a := mustAvatar(t, h, id); a != nil {
		t.Errorf("Avatar of a user with none = %+v, want nil", a)
	}
	if info := mustAvatarInfo(t, h, id); info != nil {
		t.Errorf("AvatarInfo of a user with none = %+v, want nil", info)
	}
	if a := mustAvatar(t, h, uuid.New()); a != nil {
		t.Errorf("Avatar of no user = %+v, want nil", a)
	}
}

// An Owner's user list and avatar read answer the effective avatar: a user
// with only a managed avatar is listed with one and served Staff's, a user
// with both is served Staff's.
func TestManagedAvatar_TheOwnerSeesTheEffectiveAvatar(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	onlyManaged := h.createUser(t, owner, "only@example.test", identity.RoleUser)
	both := h.createUser(t, owner, "both@example.test", identity.RoleUser)
	bothClient := h.login(t, "both@example.test", userPassword)
	ct, body := avatarUpload(t, "image/png", testPNG(t, 2, 2))
	if r := bothClient.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusOK {
		t.Fatalf("own upload: status %d body %s", r.status, r.body)
	}
	p := h.accounts(t)
	photo := testJPEG(t, 6, 6)
	for _, id := range []uuid.UUID{onlyManaged, both} {
		if err := p.SetManagedAvatar(context.Background(), systemActor, id, staffPhoto("image/jpeg", photo)); err != nil {
			t.Fatalf("SetManagedAvatar(%s): %v", id, err)
		}
	}

	listed := map[uuid.UUID]ownerUser{}
	for _, u := range ownerUsers(t, owner) {
		listed[u.ID] = u
	}
	for _, id := range []uuid.UUID{onlyManaged, both} {
		u, ok := listed[id]
		if !ok || u.AvatarURL == nil || *u.AvatarURL != userPath(id, "/avatar") {
			t.Errorf("%s listed as %+v, want avatarUrl %s", id, u, userPath(id, "/avatar"))
			continue
		}
		r := owner.do(http.MethodGet, *u.AvatarURL, nil)
		if r.status != http.StatusOK || r.header("Content-Type") != "image/jpeg" || !bytes.Equal(r.body, photo) {
			t.Errorf("owner read of %s: status %d Content-Type %q; want Staff's JPEG", id, r.status, r.header("Content-Type"))
		}
	}
}

// sameJSON reports whether got and want are the same JSON value.
func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	return bytes.Equal(gb, wb)
}

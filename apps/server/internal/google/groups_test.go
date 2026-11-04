package google_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
)

// The groups, their traits, and the photo (Google Workspace design G20,
// G21).

func TestGroupTraits_TheThreeAndTheCachedLicences(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	traits, err := l.p.GroupTraits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, tr := range traits {
		keys = append(keys, tr.Key)
		if tr.Label["en"] == "" || tr.Label["nb"] == "" || tr.Description["en"] == "" || tr.Description["nb"] == "" {
			t.Errorf("trait %s lacks a label or a description in a language", tr.Key)
		}
	}
	if !slices.Equal(keys, []string{"email", "visibility", "licences"}) {
		t.Fatalf("the traits: %v", keys)
	}
	if len(traits[2].Choices) != 0 {
		t.Errorf("licences before they were read: %v", traits[2].Choices)
	}
	m, _ := manager(t, l.h)
	r := m.Do(http.MethodGet, "/api/v1/google/connection/skus", nil)
	var skus []gen.GoogleSku
	r.JSON(&skus)
	if r.Status != http.StatusOK || len(skus) != 1 || skus[0].Value != licenceStandard || skus[0].Assigned != 1 ||
		skus[0].Name != "Google Workspace Business Standard" {
		t.Fatalf("GET /connection/skus: %d %+v", r.Status, skus)
	}
	traits, _ = l.p.GroupTraits(context.Background())
	if len(traits[2].Choices) != 1 || traits[2].Choices[0].Value != licenceStandard {
		t.Errorf("the licences' choices: %+v", traits[2].Choices)
	}
	if conn := getConnection(t, m); len(conn.Skus) != 1 {
		t.Errorf("the connection's licences: %+v", conn.Skus)
	}
}

func TestEnsureGroup_MakesTheGroupWithItsAddressVisibilityAndOwners(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	owner := l.prepare(t, joiner("E1042"))
	spec := contracts.GroupSpec{GroupID: "7", Key: "radgivning", Name: "Rådgivning", Description: "Rådgiverne", OwnerExternalIDs: []string{owner.ExternalID}}
	g, err := l.p.EnsureGroup(context.Background(), staffActor, spec)
	if err != nil || !g.Created || g.ExternalID == "" {
		t.Fatalf("EnsureGroup: %+v %v", g, err)
	}
	made, _ := l.g.Group(g.ExternalID)
	if made.Email != "radgivning@nordlys.no" || made.Name != "Rådgivning" || made.Description != "Rådgiverne" ||
		made.Settings["whoCanViewGroup"] != "ALL_IN_DOMAIN_CAN_VIEW" || made.Settings["showInGroupDirectory"] != "true" ||
		made.Members[owner.ExternalID] != "OWNER" {
		t.Fatalf("the group made: %+v", made)
	}
	// Members only, and a member who is no owner: SetMembers keeps the owner.
	spec.Values = map[string]any{"visibility": "members"}
	member := l.prepare(t, func() contracts.ExternalSpec {
		s := joiner("E2000")
		s.GivenName, s.WorkEmail = "Per", "per@nordlys.no"
		return s
	}())
	if g2, err := l.p.EnsureGroup(context.Background(), staffActor, spec); err != nil || g2.Created || g2.ExternalID != g.ExternalID {
		t.Fatalf("EnsureGroup again: %+v %v", g2, err)
	}
	if made, _ := l.g.Group(g.ExternalID); made.Settings["whoCanViewMembership"] != "ALL_MEMBERS_CAN_VIEW" || made.Settings["showInGroupDirectory"] != "false" {
		t.Errorf("members only: %v", made.Settings)
	}
	res, err := l.p.SetMembers(context.Background(), staffActor, "7", []string{member.ExternalID, "123", "not-an-id"})
	if err != nil || res.Added != 1 || len(res.Failed) != 2 {
		t.Fatalf("SetMembers: %+v %v", res, err)
	}
	for _, f := range res.Failed {
		if f.Code != contracts.MemberNotFound {
			t.Errorf("a failure: %+v", f)
		}
	}
	if made, _ := l.g.Group(g.ExternalID); made.Members[owner.ExternalID] != "OWNER" || made.Members[member.ExternalID] != "MEMBER" {
		t.Errorf("the members: %v", made.Members)
	}
	res, err = l.p.SetMembers(context.Background(), staffActor, "7", nil)
	if err != nil || res.Removed != 1 {
		t.Errorf("SetMembers to none: %+v %v", res, err)
	}
	if made, _ := l.g.Group(g.ExternalID); len(made.Members) != 1 || made.Members[owner.ExternalID] != "OWNER" {
		t.Errorf("after SetMembers to none: %v", made.Members)
	}
	// The owner no longer named is lowered to a member.
	spec.OwnerExternalIDs = nil
	if _, err := l.p.EnsureGroup(context.Background(), staffActor, spec); err != nil {
		t.Fatal(err)
	}
	if made, _ := l.g.Group(g.ExternalID); made.Members[owner.ExternalID] != "MEMBER" {
		t.Errorf("the former owner: %v", made.Members)
	}
	// The address cannot change.
	spec.Values = map[string]any{"email": "andre@nordlys.no"}
	var traitErr *contracts.GroupTraitError
	if _, err := l.p.EnsureGroup(context.Background(), staffActor, spec); !errors.As(err, &traitErr) || traitErr.Code != contracts.TraitImmutable || traitErr.Field != "email" {
		t.Errorf("a changed address: %v", err)
	}
	// An address off the verified domains is invalid.
	spec = contracts.GroupSpec{GroupID: "8", Key: "x", Name: "X", Values: map[string]any{"email": "x@elsewhere.example"}}
	if _, err := l.p.EnsureGroup(context.Background(), staffActor, spec); !errors.As(err, &traitErr) || traitErr.Code != contracts.TraitInvalid {
		t.Errorf("an address off the domains: %v", err)
	}
	// DeleteGroup deletes the module's own.
	if err := l.p.DeleteGroup(context.Background(), staffActor, "7"); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.g.Group(g.ExternalID); ok {
		t.Error("the module's group is still there")
	}
	if err := l.p.DeleteGroup(context.Background(), staffActor, "7"); err != nil {
		t.Errorf("DeleteGroup again: %v", err)
	}
	if _, err := l.p.SetMembers(context.Background(), staffActor, "7", nil); !errors.Is(err, contracts.ErrGroupNotManageable) {
		t.Errorf("SetMembers of a group never made: %v", err)
	}
}

func TestEnsureGroup_AGroupFoundIsAdoptedUntrustedUntilConfirmed(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	found := l.g.AddGroup(googletest.Group{Email: "salg@nordlys.no", Name: "Salg (old)"})
	spec := contracts.GroupSpec{GroupID: "9", Key: "salg", Name: "Salg"}
	if _, err := l.p.EnsureGroup(context.Background(), staffActor, spec); !errors.Is(err, contracts.ErrGroupUnconfirmed) {
		t.Fatalf("EnsureGroup of a found group: %v", err)
	}
	if _, err := l.p.SetMembers(context.Background(), staffActor, "9", nil); !errors.Is(err, contracts.ErrGroupUnconfirmed) {
		t.Errorf("SetMembers of an unconfirmed group: %v", err)
	}
	if g, _ := l.g.Group(found.ID); g.Name != "Salg (old)" {
		t.Error("an unconfirmed group was written to")
	}
	m, _ := manager(t, l.h)
	r := m.Do(http.MethodGet, "/api/v1/google/groups", nil)
	var groups []gen.GoogleGroup
	r.JSON(&groups)
	if r.Status != http.StatusOK || len(groups) != 1 || groups[0].CreatedByUs || groups[0].AdoptionConfirmedAt != nil || groups[0].StaffGroupId != "9" {
		t.Fatalf("GET /groups: %d %+v", r.Status, groups)
	}
	for range 2 {
		if r := m.Do(http.MethodPost, "/api/v1/google/groups/"+found.ID+"/confirm", nil); r.Status != http.StatusOK {
			t.Fatalf("confirm: %d %s", r.Status, r.Body)
		}
	}
	if ev := l.events(t, "google.group.adoption_confirmed"); len(ev) != 1 {
		t.Errorf("the confirmation's events: %v", ev)
	}
	if r := m.Do(http.MethodPost, "/api/v1/google/groups/zz0unknown/confirm", nil); r.Status != http.StatusNotFound {
		t.Errorf("confirm an unknown group: %d", r.Status)
	}
	if _, err := l.p.EnsureGroup(context.Background(), staffActor, spec); err != nil {
		t.Fatal(err)
	}
	if g, _ := l.g.Group(found.ID); g.Name != "Salg" {
		t.Errorf("the confirmed group: %+v", g)
	}
	// Let go: the organisation keeps a group the module did not make.
	if err := l.p.DeleteGroup(context.Background(), staffActor, "9"); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.g.Group(found.ID); !ok {
		t.Error("the module deleted a group it did not make")
	}
	if ev := l.events(t, "google.group.deleted"); len(ev) != 1 || ev[0][0] != "kept_not_ours" {
		t.Errorf("the delete's events: %v", ev)
	}
}

// photo is a w×h picture of one colour, encoded as format.
func photo(t *testing.T, w, h int, format string) contracts.Photo {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if format == "png" {
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return contracts.Photo{ContentType: "image/png", Data: buf.Bytes()}
	}
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return contracts.Photo{ContentType: "image/jpeg", Data: buf.Bytes()}
}

func TestPhoto_WrittenAt96PixelsAndClearedAndRefused(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	if err := l.p.SetPhoto(context.Background(), staffActor, acc.ExternalID, photo(t, 640, 480, "png")); err != nil {
		t.Fatal(err)
	}
	u := l.user(t, acc.ExternalID)
	if u.PhotoMimeType != "JPEG" || u.PhotoWidth != 96 {
		t.Fatalf("the photo written: %s %d", u.PhotoMimeType, u.PhotoWidth)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(u.Photo))
	if err != nil || format != "jpeg" || cfg.Width != 96 || cfg.Height != 96 {
		t.Errorf("the photo decoded: %v %s %+v", err, format, cfg)
	}
	if err := l.p.ClearPhoto(context.Background(), staffActor, acc.ExternalID); err != nil {
		t.Fatal(err)
	}
	if l.user(t, acc.ExternalID).Photo != nil {
		t.Error("the photo is still there")
	}
	if err := l.p.ClearPhoto(context.Background(), staffActor, acc.ExternalID); err != nil {
		t.Errorf("a clear of no photo: %v", err)
	}
	// Refused, and undecodable: reported, not failed.
	l.g.Configure(func(g *googletest.FakeGoogle) { g.RefusePhoto = true })
	if err := l.p.SetPhoto(context.Background(), staffActor, acc.ExternalID, photo(t, 50, 50, "jpeg")); err != nil {
		t.Fatal(err)
	}
	got, _ := l.p.Account(context.Background(), acc.ExternalID)
	if !slices.Contains(got.Reported, contracts.ReportedPhotoNotWritten) {
		t.Errorf("a refused photo: %v", got.Reported)
	}
	l.g.Configure(func(g *googletest.FakeGoogle) { g.RefusePhoto = false })
	if err := l.p.SetPhoto(context.Background(), staffActor, acc.ExternalID, photo(t, 50, 50, "jpeg")); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.p.Account(context.Background(), acc.ExternalID); slices.Contains(got.Reported, contracts.ReportedPhotoNotWritten) {
		t.Errorf("a photo written still reports the refusal: %v", got.Reported)
	}
	if err := l.p.SetPhoto(context.Background(), staffActor, acc.ExternalID, contracts.Photo{ContentType: "image/png", Data: []byte("not a picture")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.p.Account(context.Background(), acc.ExternalID); !slices.Contains(got.Reported, contracts.ReportedPhotoNotWritten) {
		t.Errorf("an undecodable photo: %v", got.Reported)
	}
}

func TestPhoto_AnAdministratorIsRefusedNothingWritten(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.g.SetUser(acc.ExternalID, func(u *googletest.User) { u.IsAdmin = true })
	from := len(l.g.Calls())
	if err := l.p.SetPhoto(context.Background(), staffActor, acc.ExternalID, photo(t, 50, 50, "jpeg")); !errors.Is(err, contracts.ErrExternalPrivileged) {
		t.Errorf("SetPhoto of an administrator: %v", err)
	}
	if err := l.p.ClearPhoto(context.Background(), staffActor, acc.ExternalID); !errors.Is(err, contracts.ErrExternalPrivileged) {
		t.Errorf("ClearPhoto of an administrator: %v", err)
	}
	if w := l.writes(from); len(w) != 0 {
		t.Errorf("an administrator's photo was written: %v", w)
	}
	if u := l.user(t, acc.ExternalID); u.Photo != nil {
		t.Error("an administrator carries a photo")
	}
}

package modtest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake provider's photo (staff photo sync design S2): SetPhoto stores a
// copy, ClearPhoto removes it and is idempotent, a refused photo is the
// reported photo_not_written — kept until a push succeeds or a clear — and
// both are recorded writes that FailWith shapes and an unconfirmed adoption
// refuses.
func TestFakeIdentityProvisioner_Photo(t *testing.T) {
	ctx := context.Background()
	f, _ := newTestProvisioner()
	acc, err := f.Prepare(ctx, fakeWorker, contracts.ExternalSpec{EmployeeNumber: "E-1", GivenName: "Kari", FamilyName: "Nordmann"})
	if err != nil {
		t.Fatal(err)
	}
	reported := func() []string {
		got, _ := f.ByNumber("E-1")
		return got.Reported
	}

	data := []byte{0xff, 0xd8, 0xff}
	if err := f.SetPhoto(ctx, fakeWorker, acc.ExternalID, contracts.Photo{ContentType: "image/jpeg", Data: data}); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	data[0] = 0 // the fake keeps its own copy
	if got, ok := f.Photo(acc.ExternalID); !ok || got.ContentType != "image/jpeg" || !slices.Equal(got.Data, []byte{0xff, 0xd8, 0xff}) {
		t.Errorf("Photo after SetPhoto = %+v, %v, want the JPEG as given", got, ok)
	}

	f.RefusePhoto("E-1", true)
	if err := f.SetPhoto(ctx, fakeWorker, acc.ExternalID, contracts.Photo{ContentType: "image/png", Data: []byte{0x89}}); err != nil {
		t.Errorf("a refused SetPhoto = %v, want nil: a refusal is a reported code", err)
	}
	if got, _ := f.Photo(acc.ExternalID); got.ContentType != "image/jpeg" {
		t.Errorf("a refused SetPhoto stored %q, want the JPEG kept", got.ContentType)
	}
	if got := reported(); !slices.Equal(got, []string{contracts.ReportedPhotoNotWritten}) {
		t.Errorf("reported after a refusal = %v, want [photo_not_written]", got)
	}
	if err := f.ClearPhoto(ctx, fakeWorker, acc.ExternalID); err != nil {
		t.Errorf("a refused ClearPhoto = %v, want nil: a refusal is a reported code", err)
	}
	if got, ok := f.Photo(acc.ExternalID); !ok || got.ContentType != "image/jpeg" {
		t.Errorf("a refused ClearPhoto removed the photo: %+v, %v", got, ok)
	}
	if got := reported(); !slices.Equal(got, []string{contracts.ReportedPhotoNotWritten}) {
		t.Errorf("reported after a refused clear = %v, want [photo_not_written]", got)
	}
	f.RefusePhoto("E-1", false)
	if err := f.SetPhoto(ctx, fakeWorker, acc.ExternalID, contracts.Photo{ContentType: "image/png", Data: []byte{0x89}}); err != nil {
		t.Fatal(err)
	}
	if got := reported(); len(got) != 0 {
		t.Errorf("reported after a push succeeded = %v, want none", got)
	}

	for range 2 {
		if err := f.ClearPhoto(ctx, fakeWorker, acc.ExternalID); err != nil {
			t.Errorf("ClearPhoto: %v, want nil (idempotent)", err)
		}
	}
	if _, ok := f.Photo(acc.ExternalID); ok {
		t.Error("Photo after ClearPhoto: one held, want none")
	}

	boom := errors.New("boom")
	f.FailWith("SetPhoto", boom)
	if err := f.SetPhoto(ctx, fakeWorker, acc.ExternalID, contracts.Photo{}); !errors.Is(err, boom) {
		t.Errorf("SetPhoto with FailWith = %v, want boom", err)
	}
	f.FailWith("SetPhoto", nil)
	f.SetUnconfirmed("E-1", true)
	if err := f.ClearPhoto(ctx, fakeWorker, acc.ExternalID); !errors.Is(err, contracts.ErrExternalUnconfirmed) {
		t.Errorf("ClearPhoto on an unconfirmed adoption = %v, want ErrExternalUnconfirmed", err)
	}
	if err := f.SetPhoto(ctx, contracts.Actor{}, acc.ExternalID, contracts.Photo{}); !errors.Is(err, contracts.ErrInvalidActor) {
		t.Errorf("SetPhoto with no actor = %v, want ErrInvalidActor", err)
	}

	want := []string{"Prepare", "SetPhoto", "SetPhoto", "ClearPhoto", "SetPhoto", "ClearPhoto", "ClearPhoto", "SetPhoto", "ClearPhoto"}
	if got := methods(f.Calls()); !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

// The fake avatars answer the effective avatar as identity does: the managed
// one in front of the user's own, which shows again once it is cleared, each
// with its stamp; nil for a user with none; and FailWith's error.
func TestFakeAvatars_TheEffectiveAvatar(t *testing.T) {
	ctx := context.Background()
	a := NewFakeAvatars()
	kari, ola := uuid.New(), uuid.New()

	if got, err := a.Avatar(ctx, kari); got != nil || err != nil {
		t.Errorf("Avatar with none = %+v, %v, want nil", got, err)
	}
	a.SetOwn(kari, "image/png", []byte{0x89})
	a.SetOwn(kari, "image/png", []byte{0x89, 0x50})
	got, _ := a.Avatar(ctx, kari)
	if got == nil || got.Stamp != "own:2@2" || got.ManagedBy != "" || !slices.Equal(got.Data, []byte{0x89, 0x50}) {
		t.Errorf("the own avatar = %+v, want own:2@2 and the second bytes", got)
	}
	if info, _ := a.AvatarInfo(ctx, kari); info == nil || info.Stamp != "own:2@2" || info.ContentType != "image/png" {
		t.Errorf("AvatarInfo = %+v, want own:2@2 without the bytes", info)
	}
	a.SetManaged(kari, &contracts.ManagedAvatar{ContentType: "image/jpeg", Data: []byte{0xff}, Source: "staff", Stamp: "2026-10-25T10:00:00Z"})
	got, _ = a.Avatar(ctx, kari)
	if got == nil || got.Stamp != "managed:2026-10-25T10:00:00Z" || got.ManagedBy != "staff" || got.ContentType != "image/jpeg" {
		t.Errorf("the managed avatar = %+v, want it in front", got)
	}
	a.SetManaged(kari, nil)
	if got, _ = a.Avatar(ctx, kari); got == nil || got.Stamp != "own:2@2" {
		t.Errorf("after the clear = %+v, want the own avatar again", got)
	}

	boom := errors.New("boom")
	a.FailWith(boom)
	if _, err := a.Avatar(ctx, ola); !errors.Is(err, boom) {
		t.Errorf("Avatar with FailWith = %v, want boom", err)
	}
	if asked := a.Asked(); len(asked) != 6 || asked[5] != ola {
		t.Errorf("asked = %v, want six calls, the last for ola", asked)
	}

	// Removed then uploaded again: version 1 once more, but a new stamp.
	a.FailWith(nil)
	a.SetOwn(ola, "image/png", []byte{0x89})
	first := a.OwnStamp(ola)
	a.SetOwn(ola, "image/png", nil)
	a.SetOwn(ola, "image/png", []byte{0x89})
	if again := a.OwnStamp(ola); again == first || first != "own:1@3" || again != "own:1@4" {
		t.Errorf("the stamps across a removal: %q then %q, want own:1@3 then own:1@4", first, again)
	}

	d := a.Wrap(silentDirectory{})
	a.FailWith(nil)
	if got, err := d.Avatar(ctx, kari); err != nil || got == nil || got.Stamp != "own:2@2" {
		t.Errorf("the wrapped directory's Avatar = %+v, %v, want the fake's answer", got, err)
	}
	if got, err := d.AvatarInfo(ctx, kari); err != nil || got == nil || got.Stamp != "own:2@2" {
		t.Errorf("the wrapped directory's AvatarInfo = %+v, %v, want the fake's answer", got, err)
	}
}

// silentDirectory is a contracts.UserDirectory whose Avatar would answer
// nothing: the wrap's must win.
type silentDirectory struct{ contracts.UserDirectory }

func (silentDirectory) Avatar(context.Context, uuid.UUID) (*contracts.AvatarEntry, error) {
	return nil, errors.New("silentDirectory: Avatar reached")
}

func (silentDirectory) AvatarInfo(context.Context, uuid.UUID) (*contracts.AvatarInfo, error) {
	return nil, errors.New("silentDirectory: AvatarInfo reached")
}

package modtest

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeAvatars is the avatars side of a contracts.UserDirectory a test shapes
// (staff photo sync design S1): each user's own avatar and the managed one
// standing in front of it, so a module's reads of the effective avatar can be
// tested without identity's tables. Wrap puts it in front of identity's
// directory (WithUsers(avatars.Wrap)), every other method still identity's
// own; Avatar answers the managed avatar when one stands, else the user's
// own, else nil — the Stamp "managed:<stamp>" or "own:<version>@<n>" (n a
// counter of SetOwn calls standing for identity's write time, so a removal
// then an upload never repeats a stamp) and
// ManagedBy "staff" or "", as identity answers them. SetManaged is what an
// AccountProvisioning fake calls to mirror SetManagedAvatar. Every Avatar
// call is recorded (Asked). It is safe for concurrent use.
type FakeAvatars struct {
	mu      sync.Mutex
	own     map[uuid.UUID]fakeAvatar
	managed map[uuid.UUID]fakeAvatar
	asked   []uuid.UUID
	writes  int
	err     error
	now     func() time.Time
}

// fakeAvatar is one picture: its type, its bytes, its stamp (the managed
// avatar's source stamp, or the own avatar's version) and when it was set.
type fakeAvatar struct {
	contentType string
	data        []byte
	stamp       string
	version     int
	write       int
	updatedAt   time.Time
}

// NewFakeAvatars holds no avatar for anybody, on the wall clock.
func NewFakeAvatars() *FakeAvatars {
	return &FakeAvatars{own: map[uuid.UUID]fakeAvatar{}, managed: map[uuid.UUID]fakeAvatar{}, now: time.Now}
}

// SetOwn sets the user's own avatar — the next version, stamped
// "own:<version>@<n>" — or removes it with nil data.
func (a *FakeAvatars) SetOwn(userID uuid.UUID, contentType string, data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if data == nil {
		delete(a.own, userID)
		return
	}
	version := a.own[userID].version + 1
	a.writes++
	a.own[userID] = fakeAvatar{contentType: contentType, data: slices.Clone(data), version: version, write: a.writes, updatedAt: a.now()}
}

// SetManaged sets the user's managed avatar, or clears it with nil, as
// contracts.AccountProvisioning.SetManagedAvatar does.
func (a *FakeAvatars) SetManaged(userID uuid.UUID, avatar *contracts.ManagedAvatar) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if avatar == nil {
		delete(a.managed, userID)
		return
	}
	a.managed[userID] = fakeAvatar{contentType: avatar.ContentType, data: slices.Clone(avatar.Data), stamp: avatar.Stamp, updatedAt: a.now()}
}

// FailWith makes every Avatar call answer err, nil to stop.
func (a *FakeAvatars) FailWith(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err
}

// Asked is every user Avatar was asked about, in order.
func (a *FakeAvatars) Asked() []uuid.UUID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.asked)
}

// OwnStamp is the stamp Avatar answers for the user's own avatar, "" when
// they have none.
func (a *FakeAvatars) OwnStamp(userID uuid.UUID) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	o, ok := a.own[userID]
	if !ok {
		return ""
	}
	return o.ownStamp()
}

// ownStamp is an own avatar's stamp.
func (o fakeAvatar) ownStamp() string {
	return "own:" + strconv.Itoa(o.version) + "@" + strconv.Itoa(o.write)
}

// Avatar answers the user's effective avatar: the managed one, else the
// user's own, else nil.
func (a *FakeAvatars) Avatar(_ context.Context, userID uuid.UUID) (*contracts.AvatarEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, userID)
	if a.err != nil {
		return nil, a.err
	}
	if m, ok := a.managed[userID]; ok {
		return &contracts.AvatarEntry{ContentType: m.contentType, Data: slices.Clone(m.data), Stamp: "managed:" + m.stamp, ManagedBy: "staff", UpdatedAt: m.updatedAt}, nil
	}
	if o, ok := a.own[userID]; ok {
		return &contracts.AvatarEntry{ContentType: o.contentType, Data: slices.Clone(o.data), Stamp: o.ownStamp(), UpdatedAt: o.updatedAt}, nil
	}
	return nil, nil
}

// AvatarInfo answers Avatar's entry without the bytes, and is recorded as
// Avatar is (Asked).
func (a *FakeAvatars) AvatarInfo(ctx context.Context, userID uuid.UUID) (*contracts.AvatarInfo, error) {
	e, err := a.Avatar(ctx, userID)
	if e == nil || err != nil {
		return nil, err
	}
	return &contracts.AvatarInfo{ContentType: e.ContentType, Stamp: e.Stamp, ManagedBy: e.ManagedBy, UpdatedAt: e.UpdatedAt}, nil
}

// Wrap is d with Avatar answered by a, for WithUsers.
func (a *FakeAvatars) Wrap(d contracts.UserDirectory) contracts.UserDirectory {
	return avatarUsers{UserDirectory: d, avatars: a}
}

// avatarUsers is a directory whose Avatar is a FakeAvatars'.
type avatarUsers struct {
	contracts.UserDirectory
	avatars *FakeAvatars
}

func (u avatarUsers) Avatar(ctx context.Context, userID uuid.UUID) (*contracts.AvatarEntry, error) {
	return u.avatars.Avatar(ctx, userID)
}

func (u avatarUsers) AvatarInfo(ctx context.Context, userID uuid.UUID) (*contracts.AvatarInfo, error) {
	return u.avatars.AvatarInfo(ctx, userID)
}

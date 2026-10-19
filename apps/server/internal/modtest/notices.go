package modtest

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Notice is one contracts.UserDirectory.Notify call a composed module made:
// the user it named and the notice as the module composed it.
type Notice struct {
	UserID uuid.UUID
	Notice contracts.Notice
}

// noticeRecorder keeps every Notify call made through the harness's user
// directory, in call order. It is safe for concurrent use: a module notifies
// after its commit, possibly from another goroutine.
type noticeRecorder struct {
	mu      sync.Mutex
	notices []Notice
}

func (r *noticeRecorder) record(userID uuid.UUID, n contracts.Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, Notice{UserID: userID, Notice: n})
}

func (r *noticeRecorder) all() []Notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Notice, len(r.notices))
	copy(out, r.notices)
	return out
}

// recordingUsers is identity's directory, or WithUsers' wrapping of it, with
// every Notify recorded before it is passed on: identity still decides
// whether a mail goes, and the test reads what the module asked for.
type recordingUsers struct {
	contracts.UserDirectory
	notices *noticeRecorder
}

func (u recordingUsers) Notify(ctx context.Context, userID uuid.UUID, n contracts.Notice) error {
	u.notices.record(userID, n)
	return u.UserDirectory.Notify(ctx, userID, n)
}

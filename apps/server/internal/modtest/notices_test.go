package modtest

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// refusingNotify is a directory whose Notify fails, for the recorder's
// "recorded even when refused" rule.
type refusingNotify struct{ contracts.UserDirectory }

func (refusingNotify) Notify(context.Context, uuid.UUID, contracts.Notice) error {
	return errors.New("the mail driver is down")
}

// TestNotices_RecordsEveryNotifyAndIdentityStillDelivers proves the harness
// records each Notify a module makes through Deps.Users, in order, and passes
// it on to identity, which mails the active user and skips the unknown one;
// and that a call WithUsers' wrapping refuses is recorded all the same.
func TestNotices_RecordsEveryNotifyAndIdentityStillDelivers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		opts    []Option
		wantErr bool
		mails   int
	}{
		{"identity's directory", nil, false, 1},
		{"a refusing wrapper", []Option{WithUsers(func(d contracts.UserDirectory) contracts.UserDirectory { return refusingNotify{d} })}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var users contracts.UserDirectory
			capture := module.Module{
				Name: "customers",
				Mount: func(d module.Deps) (http.Handler, error) {
					users = d.Users
					return http.NotFoundHandler(), nil
				},
			}
			opts := append([]Option{WithRecorder(contracttest.NewForModule("customers")), WithModule(capture)}, tc.opts...)
			h := New(t, opts...)
			_, known := h.SignInUser(t)
			unknown := uuid.New()
			notice := contracts.Notice{Texts: map[string]contracts.NoticeText{"en": {Subject: "Hello", Body: "There"}}}

			for _, id := range []uuid.UUID{known, unknown} {
				if err := users.Notify(context.Background(), id, notice); (err != nil) != tc.wantErr {
					t.Fatalf("Notify(%s) = %v, want error %v", id, err, tc.wantErr)
				}
			}

			got := h.Notices()
			if len(got) != 2 || got[0].UserID != known || got[1].UserID != unknown || got[0].Notice.Texts["en"].Subject != "Hello" {
				t.Errorf("Notices = %+v, want the known then the unknown user's notice", got)
			}
			if sent := h.Deps().Mail.(*mail.Fake).Messages(); len(sent) != tc.mails {
				t.Errorf("mails = %+v, want %d", sent, tc.mails)
			}
		})
	}
}

package meta_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The Graph client, the Pages, Send and Profile are tested against
// metatest.FakeGraph. Helpers carry the prefix gr.

// grStore is an in-memory storage.ObjectStore.
type grStore struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newGrStore() *grStore { return &grStore{objs: map[string][]byte{}} }

func (s *grStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs[key] = b
	return nil
}

func (s *grStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objs[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *grStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objs[key]
	return ok, nil
}

func (s *grStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objs, key)
	return nil
}

// The fake Page.
const (
	grPageID    = "100200300"
	grPageName  = "Nordlys Bil"
	grPageToken = "EAAB-page-token-secret"
	grPSID      = "5550001"
)

// grEnv is one installation with the fake Graph beside it.
type grEnv struct {
	h     *harness
	owner *modtest.Client
	fake  *metatest.FakeGraph
	store *grStore
	m     contracts.ChannelMessenger
}

// newGrEnv is a configured installation (the app id and secret stored), with
// no Page.
func newGrEnv(t *testing.T) *grEnv {
	t.Helper()
	e := newGrEnvUnconfigured(t)
	configure(t, e.owner)
	return e
}

// newGrEnvUnconfigured is an installation with no app id and secret.
func newGrEnvUnconfigured(t *testing.T) *grEnv {
	t.Helper()
	fake, store := metatest.NewFakeGraph(), newGrStore()
	h := newHarness(t, modtest.WithTransport(fake), modtest.WithObjectStore(store))
	o, _ := owner(t, h)
	return &grEnv{h: h, owner: o, fake: fake, store: store, m: meta.Module().ChannelMessenger(h.Deps())}
}

// withPage registers the fake Page and connects it, which must answer 201.
func (e *grEnv) withPage(t *testing.T) *grEnv {
	t.Helper()
	e.fake.AddPage(grPageID, grPageName, grPageToken, "MESSAGING")
	if r := e.addPage(grPageToken); r.Status != http.StatusCreated {
		t.Fatalf("add the Page: status %d body %s", r.Status, r.Body)
	}
	return e
}

func (e *grEnv) addPage(token string) *modtest.Response {
	return e.owner.Do(http.MethodPost, pagesPath, map[string]any{"accessToken": token})
}

// problemCode is the code of a problem response.
func grProblemCode(t *testing.T, r *modtest.Response) string {
	t.Helper()
	var p gen.MetaProblem
	r.JSON(&p)
	if p.Code == nil {
		return ""
	}
	return *p.Code
}

// grLastCode is the status line's last Graph error code; "" while none.
func grLastCode(t *testing.T, e *grEnv) string {
	t.Helper()
	c := modtest.One[*string](t, e.h.Harness, `SELECT last_graph_error_code FROM meta.settings WHERE id = 1`)
	if c == nil {
		return ""
	}
	return *c
}

func grPageState(t *testing.T, e *grEnv) string {
	t.Helper()
	return modtest.One[string](t, e.h.Harness, `SELECT state FROM meta.pages WHERE page_id = $1`, grPageID)
}

// TestGraph_ClassifiesEveryRowOfTheErrorMap drives Messenger design N12's table.
func TestGraph_ClassifiesEveryRowOfTheErrorMap(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		status, code, subcode int
		tagged                bool
		temporary             bool
		is                    error
	}{
		{name: "code 1", status: 400, code: 1, temporary: true},
		{name: "code 2", status: 400, code: 2, temporary: true},
		{name: "code 4", status: 400, code: 4, temporary: true},
		{name: "code 613", status: 400, code: 613, temporary: true},
		{name: "a 500", status: 500, temporary: true},
		{name: "a 503", status: 503, code: 100, temporary: true},
		{name: "551", status: 400, code: 551, is: contracts.ErrRecipientUnreachable},
		{name: "10/2018278", status: 400, code: 10, subcode: 2018278, is: contracts.ErrReplyWindowClosed},
		{name: "10/2018278 tagged", status: 400, code: 10, subcode: 2018278, tagged: true, is: contracts.ErrReplyWindowClosed},
		{name: "190", status: 400, code: 190, is: contracts.ErrMessengerRejected},
		{name: "10 tagged", status: 403, code: 10, tagged: true, is: contracts.ErrHumanAgentNotPermitted},
		{name: "200 tagged", status: 403, code: 200, tagged: true, is: contracts.ErrHumanAgentNotPermitted},
		{name: "10 untagged", status: 403, code: 10, is: contracts.ErrMessengerRejected},
		{name: "200 untagged", status: 403, code: 200, is: contracts.ErrMessengerRejected},
		{name: "anything else", status: 400, code: 100, is: contracts.ErrMessengerRejected},
		{name: "200 tagged with the window subcode", status: 400, code: 200, subcode: 2018278, tagged: true, is: contracts.ErrMessengerRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := meta.GrClassify(tc.status, tc.code, tc.subcode, tc.tagged)
			if err == nil {
				t.Fatal("no error")
			}
			if got := contracts.IsMessengerTemporary(err); got != tc.temporary {
				t.Fatalf("temporary = %v, want %v (%v)", got, tc.temporary, err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("error %v is not %v", err, tc.is)
			}
			if errors.Is(tc.is, contracts.ErrMessengerRejected) && !strings.Contains(err.Error(), "Meta's words") {
				t.Fatalf("a rejection carries Meta's message: %v", err)
			}
		})
	}
}

// TestGraph_AuthenticatesByHeaderOnly: the token is in the Authorization
// header and nowhere in the URL, the logs or the answer.
func TestGraph_AuthenticatesByHeaderOnly(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	e.fake.AddUser(grPSID, "Kari Nordmann")
	if _, err := e.m.Send(context.Background(), grPageID, contracts.MessengerPart{MessageID: "m1", Participant: grPSID, Text: "hemmelig tekst"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Profile(context.Background(), grPageID, grPSID); err != nil {
		t.Fatal(err)
	}
	calls := e.fake.Calls()
	if len(calls) < 4 {
		t.Fatalf("calls: %+v", calls)
	}
	for _, c := range calls {
		if c.Token != grPageToken {
			t.Errorf("%s %s: bearer %q", c.Method, c.Path, c.Token)
		}
		if strings.Contains(c.URL, grPageToken) || c.Query.Has("access_token") {
			t.Errorf("%s: the token is in the URL %q", c.Path, c.URL)
		}
	}
	logs := e.h.Logs()
	for _, secret := range []string{grPageToken, "hemmelig tekst", grPSID} {
		if strings.Contains(logs, secret) {
			t.Errorf("the logs hold %q", secret)
		}
	}
}

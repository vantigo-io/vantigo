package knowledge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one Knowledge installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against knowledge.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(knowledge.Module()),
	}, opts...)...)
}

// person is a signed-in user of Knowledge.
type person struct {
	c  *modtest.Client
	id uuid.UUID
}

func signIn(t *testing.T, h *modtest.Harness, permissions ...string) person {
	t.Helper()
	c, id := h.SignInUser(t, append([]string{"knowledge:articles"}, permissions...)...)
	return person{c: c, id: id}
}

// articleJSON is the parts of KnowledgeArticle the tests read.
type articleJSON struct {
	ID                string         `json:"id"`
	ParentID          *string        `json:"parentId"`
	Position          string         `json:"position"`
	Title             string         `json:"title"`
	Version           int32          `json:"version"`
	Locked            bool           `json:"locked"`
	AiHidden          bool           `json:"aiHidden"`
	AiHiddenInherited bool           `json:"aiHiddenInherited"`
	Permission        string         `json:"permission"`
	Section           string         `json:"section"`
	Path              []pathItemJSON `json:"path"`
	UpdatedBy         *actorJSON     `json:"updatedBy"`
	Content           map[string]any `json:"content"`
}

type pathItemJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type actorJSON struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
}

type problemJSON struct {
	Code           string     `json:"code"`
	Path           string     `json:"path"`
	CurrentVersion int32      `json:"currentVersion"`
	UpdatedBy      *actorJSON `json:"updatedBy"`
	UpdatedAt      *time.Time `json:"updatedAt"`
}

func problemOf(t *testing.T, r *modtest.Response) problemJSON {
	t.Helper()
	var p problemJSON
	if err := json.Unmarshal(r.Body, &p); err != nil {
		t.Fatalf("problem body %s: %v", r.Body, err)
	}
	return p
}

// expectStatus fails unless r answered status, and with code when one is
// given.
func expectStatus(t *testing.T, what string, r *modtest.Response, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: status %d body %s, want %d %s", what, r.Status, r.Body, status, code)
	}
	if code != "" {
		if got := problemOf(t, r).Code; got != code {
			t.Fatalf("%s: code %q body %s, want %q", what, got, r.Body, code)
		}
	}
}

// paragraph is a document of one paragraph of text.
func paragraph(text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}},
	}}
}

// create makes an article as p; parent "" makes a root in section.
func create(t *testing.T, p person, parent, section, title string) articleJSON {
	t.Helper()
	body := map[string]any{"title": title}
	if parent != "" {
		body["parentId"] = parent
	} else if section != "" {
		body["section"] = section
	}
	r := p.c.Do(http.MethodPost, "/api/v1/knowledge/articles", body)
	expectStatus(t, "create "+title, r, http.StatusCreated, "")
	var a articleJSON
	r.JSON(&a)
	return a
}

func get(t *testing.T, p person, id string) (*modtest.Response, articleJSON) {
	t.Helper()
	r := p.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+id, nil)
	var a articleJSON
	if r.Status == http.StatusOK {
		r.JSON(&a)
	}
	return r, a
}

// save is an autosave of p over version.
func save(p person, id, title, text string, version int32) *modtest.Response {
	return p.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+id+"/content", map[string]any{
		"title": title, "content": paragraph(text), "expectedVersion": version,
	})
}

// setAccess replaces an article's own default and members.
func setAccess(p person, id string, internal any, members map[uuid.UUID]string, confirm bool) *modtest.Response {
	list := []map[string]any{}
	for u, perm := range members {
		list = append(list, map[string]any{"userId": u, "permission": perm})
	}
	body := map[string]any{"internalPermission": internal, "members": list}
	if confirm {
		body["confirmVisibility"] = true
	}
	return p.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+id+"/access", body)
}

// move moves an article.
func move(p person, id string, parent, after any, confirm bool) *modtest.Response {
	body := map[string]any{"parentId": parent, "afterId": after}
	if confirm {
		body["confirmVisibility"] = true
	}
	return p.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+id+"/move", body)
}

// liveEvent is one notification on live.Channel.
type liveEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// liveListener LISTENs on the harness's database, as a hub does.
type liveListener struct{ events chan liveEvent }

func listenLive(t *testing.T, h *modtest.Harness) *liveListener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect the listener: %v", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &liveListener{events: make(chan liveEvent, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				l.events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return l
}

// expect waits for every one of want, in any order, among what arrives, and
// answers everything heard until then.
func (l *liveListener) expect(t *testing.T, want ...liveEvent) []liveEvent {
	t.Helper()
	missing := map[liveEvent]bool{}
	for _, w := range want {
		missing[w] = true
	}
	deadline := time.After(10 * time.Second)
	var seen []liveEvent
	for len(missing) > 0 {
		select {
		case e := <-l.events:
			seen = append(seen, e)
			delete(missing, e)
		case <-deadline:
			t.Fatalf("live events missing %v; heard %v", missing, seen)
		}
	}
	return seen
}

// drain answers what arrives within a short while.
func (l *liveListener) drain(wait time.Duration) []liveEvent {
	var seen []liveEvent
	deadline := time.After(wait)
	for {
		select {
		case e := <-l.events:
			seen = append(seen, e)
		case <-deadline:
			return seen
		}
	}
}

func articleEvent(id, kind string) liveEvent {
	return liveEvent{T: "knowledge.article:" + id, K: kind, ID: id}
}

var treeEvent = liveEvent{T: "knowledge.tree", K: "tree.changed"}

package knowledge_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Every status code of K6 and the tree's refusals (K3, K4, K8, K11a).
func TestArticles_StatusCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)

	w := create(t, a, "", "workspace", "Rutiner")
	if w.Section != "workspace" || w.Permission != "write" || w.Version != 1 {
		t.Fatalf("a workspace root = %+v", w)
	}
	c := create(t, a, w.ID, "", "Fakturering")
	if c.ParentID == nil || *c.ParentID != w.ID || len(c.Path) != 1 || c.Path[0].ID != w.ID {
		t.Fatalf("a child = %+v", c)
	}

	// B reads and writes the workspace article.
	if r, got := get(t, b, w.ID); r.Status != http.StatusOK || got.Permission != "write" {
		t.Fatalf("B reads the workspace article: %d %s", r.Status, r.Body)
	}

	// A makes it read-only for everyone but themself: B reads, cannot write.
	expectStatus(t, "make read-only", setAccess(a, w.ID, "read", map[uuid.UUID]string{a.id: "write"}, false), http.StatusOK, "")
	if r, got := get(t, b, c.ID); r.Status != http.StatusOK || got.Permission != "read" {
		t.Fatalf("B reads the inherited child: %d %s", r.Status, r.Body)
	}
	expectStatus(t, "B saves", save(b, c.ID, "x", "y", 1), http.StatusForbidden, "article_read_only")
	expectStatus(t, "B renames", b.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+c.ID, map[string]any{"title": "x"}), http.StatusForbidden, "article_read_only")
	expectStatus(t, "B trashes", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+c.ID+"/trash", nil), http.StatusForbidden, "article_read_only")
	expectStatus(t, "B moves", move(b, c.ID, nil, nil, false), http.StatusForbidden, "article_read_only")
	expectStatus(t, "B changes access", setAccess(b, c.ID, nil, nil, false), http.StatusForbidden, "article_read_only")
	expectStatus(t, "B creates under", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"parentId": w.ID}), http.StatusForbidden, "article_read_only")

	// A private article does not exist for B (404, never 403).
	p := create(t, a, "", "private", "Mine")
	if p.Section != "private" {
		t.Fatalf("a private root's section = %q", p.Section)
	}
	for _, r := range []*modtest.Response{
		b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID, nil),
		save(b, p.ID, "x", "y", 1),
		b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID+"/access", nil),
		b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID+"/text", nil),
		b.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID+"/versions", nil),
		b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+p.ID+"/trash", nil),
		b.c.Do(http.MethodGet, "/api/v1/knowledge/tree?parentId="+p.ID, nil),
		b.c.Do(http.MethodPut, "/api/v1/knowledge/favourites/"+p.ID, nil),
	} {
		expectStatus(t, "B reaches a private article", r, http.StatusNotFound, "")
	}

	// The lock refuses content edits (409 article_locked) until unlocked.
	r := a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+c.ID, map[string]any{"locked": true})
	expectStatus(t, "lock", r, http.StatusOK, "")
	expectStatus(t, "save a locked article", save(a, c.ID, "Fakturering", "x", 1), http.StatusConflict, "article_locked")
	expectStatus(t, "unlock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+c.ID, map[string]any{"locked": false}), http.StatusOK, "")

	// A stale version is version_conflict with who saved the current one.
	expectStatus(t, "save", save(a, c.ID, "Fakturering", "first", 1), http.StatusOK, "")
	r = save(a, c.ID, "Fakturering", "stale", 1)
	expectStatus(t, "a stale save", r, http.StatusConflict, "version_conflict")
	if pr := problemOf(t, r); pr.CurrentVersion != 2 || pr.UpdatedBy == nil || pr.UpdatedBy.UserID != a.id.String() || pr.UpdatedAt == nil {
		t.Errorf("version_conflict = %+v, want version 2 by A", pr)
	}

	// The document's allowlist and size (K8).
	r = a.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+c.ID+"/content", map[string]any{
		"title": "x", "expectedVersion": 2,
		"content": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"src": "x"}}}},
	})
	expectStatus(t, "an unknown node", r, http.StatusBadRequest, "invalid_document")
	if got := problemOf(t, r).Path; got != "$.content[0].type" {
		t.Errorf("invalid_document path = %q", got)
	}
	expectStatus(t, "a document over 2 MiB", save(a, c.ID, "x", strings.Repeat("a", 2<<20), 2), http.StatusRequestEntityTooLarge, "document_too_large")

	// The tree's refusals.
	expectStatus(t, "move under itself", move(a, w.ID, c.ID, nil, false), http.StatusBadRequest, "move_into_descendant")
	expectStatus(t, "move under itself, directly", move(a, w.ID, w.ID, nil, false), http.StatusBadRequest, "move_into_descendant")
	expectStatus(t, "an unknown parent", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"parentId": uuid.NewString()}), http.StatusBadRequest, "parent_invalid")
	expectStatus(t, "an after that is no sibling", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"parentId": w.ID, "afterId": w.ID}), http.StatusBadRequest, "after_invalid")
	parent := w.ID
	for range 8 { // w is level 1, c level 2; this makes levels 2..9 under w
		parent = create(t, a, parent, "", "deep").ID
	}
	tenth := create(t, a, parent, "", "level 10")
	expectStatus(t, "an 11th level", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"parentId": tenth.ID}), http.StatusBadRequest, "too_deep")
	expectStatus(t, "a move making 11 levels", move(a, c.ID, tenth.ID, nil, false), http.StatusBadRequest, "too_deep")

	// Access refusals.
	expectStatus(t, "a root without its own default", setAccess(a, w.ID, nil, map[uuid.UUID]string{a.id: "write"}, false), http.StatusBadRequest, "root_permission_required")
	expectStatus(t, "removing the last writer", setAccess(a, p.ID, "none", map[uuid.UUID]string{a.id: "read"}, false), http.StatusBadRequest, "last_writer")
	expectStatus(t, "private to workspace, unconfirmed", setAccess(a, p.ID, "write", nil, false), http.StatusConflict, "visibility_change")
	expectStatus(t, "a private article moved under the workspace, unconfirmed", move(a, create(t, a, p.ID, "", "child of mine").ID, w.ID, nil, false), http.StatusConflict, "visibility_change")
	expectStatus(t, "private to workspace, confirmed", setAccess(a, p.ID, "write", nil, true), http.StatusOK, "")
	if _, got := get(t, b, p.ID); got.Section != "workspace" {
		t.Errorf("a published article's section = %q, want workspace", got.Section)
	}
	expectStatus(t, "restore an article not in the trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/restore", nil), http.StatusConflict, "not_trashed")
}

// The sidebar: the topmost readable article of every section and children,
// shared articles under an unreadable parent, moves after a sibling (K3, K4).
func TestTree_SectionsAndMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)

	w1 := create(t, a, "", "workspace", "One")
	w2 := create(t, a, "", "workspace", "Two")
	p := create(t, a, "", "private", "Mine")
	s := create(t, a, p.ID, "", "Shared with B")
	expectStatus(t, "share", setAccess(a, s.ID, nil, map[uuid.UUID]string{b.id: "read"}, false), http.StatusOK, "")

	roots := func(who person) map[string]string {
		r := who.c.Do(http.MethodGet, "/api/v1/knowledge/tree", nil)
		expectStatus(t, "tree", r, http.StatusOK, "")
		var tree struct {
			Items []struct {
				ID, Section, Title string
				HasChildren        bool
			} `json:"items"`
		}
		r.JSON(&tree)
		out := map[string]string{}
		for _, n := range tree.Items {
			out[n.Title] = n.Section
		}
		return out
	}
	if got := roots(a); got["One"] != "workspace" || got["Two"] != "workspace" || got["Mine"] != "private" || len(got) != 3 {
		t.Errorf("A's roots = %v", got)
	}
	if got := roots(b); got["Shared with B"] != "shared" || got["One"] != "workspace" || got["Mine"] != "" {
		t.Errorf("B's roots = %v", got)
	}
	// B's path to the shared article stops at the private parent.
	if _, got := get(t, b, s.ID); len(got.Path) != 0 {
		t.Errorf("B's path = %v, want it cut at the unreadable parent", got.Path)
	}

	// Moves: w2 first, then w1 after w2, then w1 under w2.
	r := move(a, w2.ID, nil, nil, false)
	expectStatus(t, "move first", r, http.StatusOK, "")
	expectStatus(t, "move after", move(a, w1.ID, nil, w2.ID, false), http.StatusOK, "")
	_, g1 := get(t, a, w1.ID)
	_, g2 := get(t, a, w2.ID)
	if g2.Position >= g1.Position {
		t.Errorf("positions %q, %q: w2 should come before w1", g2.Position, g1.Position)
	}
	expectStatus(t, "move under", move(a, w1.ID, w2.ID, nil, false), http.StatusOK, "")
	r = a.c.Do(http.MethodGet, "/api/v1/knowledge/tree?parentId="+w2.ID, nil)
	expectStatus(t, "children", r, http.StatusOK, "")
	if !strings.Contains(string(r.Body), w1.ID) {
		t.Errorf("children of w2 = %s", r.Body)
	}
	// A child moved to the root keeps who can read it.
	expectStatus(t, "shared child to the root", move(a, s.ID, nil, nil, false), http.StatusOK, "")
	if r, got := get(t, b, s.ID); r.Status != http.StatusOK || got.Section != "shared" {
		t.Errorf("B reads the moved shared article: %d %s", r.Status, r.Body)
	}
}

// Access: the access dialog's view, a manager's reach and the member picker
// (K2, K4).
func TestAccess_ViewManageAndPicker(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	m := signIn(t, h, "knowledge:manage")

	p := create(t, a, "", "private", "Mine")
	child := create(t, a, p.ID, "", "Child")
	expectStatus(t, "two writers", setAccess(a, p.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "write"}, false), http.StatusOK, "")

	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+child.ID+"/access", nil)
	expectStatus(t, "access", r, http.StatusOK, "")
	var acc struct {
		InternalPermission *string
		InheritedFrom      *pathItemJSON
		CanManage          bool
		Section            string
		Members            []struct {
			UserID        string `json:"userId"`
			Permission    string
			Inherited     bool
			InheritedFrom *pathItemJSON
		}
	}
	r.JSON(&acc)
	if acc.InternalPermission != nil || acc.InheritedFrom == nil || acc.InheritedFrom.ID != p.ID || !acc.CanManage || len(acc.Members) != 2 {
		t.Fatalf("access = %s", r.Body)
	}
	for _, mem := range acc.Members {
		if !mem.Inherited || mem.InheritedFrom == nil || mem.InheritedFrom.ID != p.ID {
			t.Errorf("member %+v, want inherited from the root", mem)
		}
	}

	// Two writers: not private, so the manager sees and changes it, but
	// cannot open it.
	expectStatus(t, "manager reads the access", m.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID+"/access", nil), http.StatusOK, "")
	expectStatus(t, "manager opens it", m.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID, nil), http.StatusNotFound, "")
	expectStatus(t, "manager adds a reader", setAccess(m, p.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "write", m.id: "read"}, false), http.StatusOK, "")
	expectStatus(t, "manager now reads it", m.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+p.ID, nil), http.StatusOK, "")

	// A truly private article is private from the manager too.
	mine := create(t, b, "", "private", "B's own")
	expectStatus(t, "manager reads a private article's access", m.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+mine.ID+"/access", nil), http.StatusNotFound, "")
	expectStatus(t, "manager changes a private article's access", setAccess(m, mine.ID, "write", nil, true), http.StatusNotFound, "")

	r = a.c.Do(http.MethodGet, "/api/v1/knowledge/assignable-users?limit=5", nil)
	expectStatus(t, "assignable users", r, http.StatusOK, "")
	var users []actorJSON
	r.JSON(&users)
	if len(users) == 0 || len(users) > 5 {
		t.Errorf("assignable users = %s", r.Body)
	}
	expectStatus(t, "assignable users over the cap", a.c.Do(http.MethodGet, "/api/v1/knowledge/assignable-users?limit=21", nil), http.StatusBadRequest, "")
	expectStatus(t, "an unknown member", setAccess(a, p.ID, "none", map[uuid.UUID]string{a.id: "write", uuid.New(): "read"}, false), http.StatusBadRequest, "")
}

// Versions: one person's saves within 10 minutes coalesce; anyone else's, or
// later, add a version; a restore always adds one (K9).
func TestVersions_CoalesceAndRestore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	art := create(t, a, "", "workspace", "Routine")
	versions := func() []struct {
		Version int32
		Title   string
		Current bool
	} {
		r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+art.ID+"/versions", nil)
		expectStatus(t, "versions", r, http.StatusOK, "")
		var out struct {
			Items []struct {
				Version int32
				Title   string
				Current bool
			}
		}
		r.JSON(&out)
		return out.Items
	}

	expectStatus(t, "save 2", save(a, art.ID, "Routine", "one", 1), http.StatusOK, "")
	if v := versions(); len(v) != 1 || v[0].Version != 2 || !v[0].Current {
		t.Fatalf("after a save at once = %+v, want the creation coalesced into version 2", v)
	}
	h.Advance(11 * time.Minute)
	expectStatus(t, "save 3", save(a, art.ID, "Routine", "two", 2), http.StatusOK, "")
	expectStatus(t, "save 4 by B", save(b, art.ID, "Routine", "three", 3), http.StatusOK, "")
	expectStatus(t, "save 5 by B", save(b, art.ID, "Routine v5", "four", 4), http.StatusOK, "")
	v := versions()
	if len(v) != 3 || v[0].Version != 5 || v[1].Version != 3 || v[2].Version != 2 {
		t.Fatalf("versions = %+v, want 5 (B, coalesced), 3 (A), 2 (A)", v)
	}
	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+art.ID+"/versions/3", nil)
	expectStatus(t, "version 3", r, http.StatusOK, "")
	if !strings.Contains(string(r.Body), `"two"`) {
		t.Errorf("version 3 = %s", r.Body)
	}
	expectStatus(t, "an unknown version", a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+art.ID+"/versions/4", nil), http.StatusNotFound, "")

	expectStatus(t, "restore stale", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/versions/3/restore", map[string]any{"expectedVersion": 4}), http.StatusConflict, "version_conflict")
	expectStatus(t, "restore", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/versions/3/restore", map[string]any{"expectedVersion": 5}), http.StatusOK, "")
	v = versions()
	if len(v) != 4 || v[0].Version != 6 || v[0].Title != "Routine" {
		t.Fatalf("after a restore = %+v, want a new version 6", v)
	}
	if _, got := get(t, a, art.ID); got.Version != 6 || !strings.Contains(textOf(got.Content), "two") {
		t.Errorf("the restored article = %+v", got)
	}
	// A title changed through PATCH is a version too.
	expectStatus(t, "rename", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"title": "Renamed", "icon": "📘"}), http.StatusOK, "")
	if _, got := get(t, a, art.ID); got.Version != 7 || got.Title != "Renamed" {
		t.Errorf("after a rename = %+v", got)
	}
}

func textOf(d map[string]any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if s, ok := n["text"].(string); ok {
				b.WriteString(s)
			}
			walk(n["content"])
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(d)
	return b.String()
}

// The trash: a subtree goes as one group, a child restores its whole group,
// a group whose parent is trashed goes to the root, and the trash is
// emptied by a manager or by the retention (K10, §9).
func TestTrash_Groups(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	m := signIn(t, h, "knowledge:manage")

	w := create(t, a, "", "workspace", "Top")
	c := create(t, a, w.ID, "", "Middle")
	g := create(t, a, c.ID, "", "Bottom")
	expectStatus(t, "trash the middle", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+c.ID+"/trash", nil), http.StatusNoContent, "")
	for _, id := range []string{c.ID, g.ID} {
		if r, _ := get(t, a, id); r.Status != http.StatusNotFound {
			t.Errorf("a trashed article answers %d", r.Status)
		}
	}
	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/trash", nil)
	expectStatus(t, "trash", r, http.StatusOK, "")
	var trash struct {
		Items []struct {
			ID           string
			ArticleCount int32
			Parent       *pathItemJSON
			TrashedBy    *actorJSON
		}
	}
	r.JSON(&trash)
	if len(trash.Items) != 1 || trash.Items[0].ID != c.ID || trash.Items[0].ArticleCount != 2 || trash.Items[0].Parent == nil || trash.Items[0].TrashedBy == nil {
		t.Fatalf("trash = %s", r.Body)
	}

	// Restoring the child restores its group.
	r = a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+g.ID+"/restore", nil)
	expectStatus(t, "restore the bottom", r, http.StatusOK, "")
	var restored struct {
		ID            string
		RestoredCount int32
		GroupRestored bool
		MovedToRoot   bool
	}
	r.JSON(&restored)
	if restored.ID != c.ID || restored.RestoredCount != 2 || !restored.GroupRestored || restored.MovedToRoot {
		t.Fatalf("restored = %+v", restored)
	}

	// The middle, then the top: the middle's parent is in the trash, so it
	// comes back to the root, keeping its access.
	expectStatus(t, "trash the middle again", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+c.ID+"/trash", nil), http.StatusNoContent, "")
	expectStatus(t, "trash the top", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/trash", nil), http.StatusNoContent, "")
	r = a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+c.ID+"/restore", nil)
	expectStatus(t, "restore the middle", r, http.StatusOK, "")
	r.JSON(&restored)
	if !restored.MovedToRoot || restored.GroupRestored {
		t.Fatalf("restored = %+v, want moved to the root", restored)
	}
	if r, got := get(t, b, c.ID); r.Status != http.StatusOK || got.ParentID != nil || got.Section != "workspace" {
		t.Errorf("the restored middle for B: %d %s", r.Status, r.Body)
	}

	// Another person's private trash is not B's to restore, nor to see.
	p := create(t, a, "", "private", "Mine")
	expectStatus(t, "trash mine", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+p.ID+"/trash", nil), http.StatusNoContent, "")
	expectStatus(t, "B restores it", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+p.ID+"/restore", nil), http.StatusNotFound, "")
	expectStatus(t, "the manager restores it", m.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+p.ID+"/restore", nil), http.StatusNotFound, "")
	// The manager restores a workspace group B could not write.
	expectStatus(t, "make the top A's alone", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/restore", nil), http.StatusOK, "")
	expectStatus(t, "lock B out of writing", setAccess(a, w.ID, "read", map[uuid.UUID]string{a.id: "write"}, false), http.StatusOK, "")
	expectStatus(t, "trash the top", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/trash", nil), http.StatusNoContent, "")
	expectStatus(t, "B restores the top", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/restore", nil), http.StatusNotFound, "")
	expectStatus(t, "the manager restores the top", m.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/restore", nil), http.StatusOK, "")

	// Emptying: knowledge:manage only.
	expectStatus(t, "trash the top again", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+w.ID+"/trash", nil), http.StatusNoContent, "")
	expectStatus(t, "A empties", a.c.Do(http.MethodDelete, "/api/v1/knowledge/trash", nil), http.StatusForbidden, "")
	expectStatus(t, "the manager empties", m.c.Do(http.MethodDelete, "/api/v1/knowledge/trash", nil), http.StatusNoContent, "")
	if n := h.Count(t, `SELECT count(*) FROM knowledge.articles WHERE trashed_at IS NOT NULL`); n != 0 {
		t.Errorf("%d trashed articles left", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.articles WHERE id = $1`, w.ID); n != 0 {
		t.Error("the emptied article is still there")
	}
}

// Favourites: ordered, hidden when unreadable (K11).
func TestFavourites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	x := create(t, a, "", "workspace", "X")
	y := create(t, a, "", "workspace", "Y")
	z := create(t, a, "", "workspace", "Z")
	for _, id := range []string{x.ID, y.ID, z.ID, x.ID} {
		expectStatus(t, "add", b.c.Do(http.MethodPut, "/api/v1/knowledge/favourites/"+id, nil), http.StatusNoContent, "")
	}
	order := func() []string {
		r := b.c.Do(http.MethodGet, "/api/v1/knowledge/favourites", nil)
		expectStatus(t, "favourites", r, http.StatusOK, "")
		var out struct{ Items []struct{ Title string } }
		r.JSON(&out)
		var titles []string
		for _, i := range out.Items {
			titles = append(titles, i.Title)
		}
		return titles
	}
	if got := strings.Join(order(), ""); got != "XYZ" {
		t.Fatalf("order = %s", got)
	}
	expectStatus(t, "z first", b.c.Do(http.MethodPost, "/api/v1/knowledge/favourites/"+z.ID+"/move", map[string]any{"afterId": nil}), http.StatusNoContent, "")
	expectStatus(t, "x after y", b.c.Do(http.MethodPost, "/api/v1/knowledge/favourites/"+x.ID+"/move", map[string]any{"afterId": y.ID}), http.StatusNoContent, "")
	if got := strings.Join(order(), ""); got != "ZYX" {
		t.Fatalf("order = %s", got)
	}
	expectStatus(t, "after an unknown favourite", b.c.Do(http.MethodPost, "/api/v1/knowledge/favourites/"+x.ID+"/move", map[string]any{"afterId": uuid.NewString()}), http.StatusBadRequest, "after_invalid")
	expectStatus(t, "move a non-favourite", a.c.Do(http.MethodPost, "/api/v1/knowledge/favourites/"+x.ID+"/move", map[string]any{"afterId": nil}), http.StatusNotFound, "")
	expectStatus(t, "remove", b.c.Do(http.MethodDelete, "/api/v1/knowledge/favourites/"+y.ID, nil), http.StatusNoContent, "")
	expectStatus(t, "remove again", b.c.Do(http.MethodDelete, "/api/v1/knowledge/favourites/"+y.ID, nil), http.StatusNotFound, "")

	// Shut out of z, B no longer sees it, and the retention removes it.
	expectStatus(t, "shut B out", setAccess(a, z.ID, "write", map[uuid.UUID]string{b.id: "none"}, false), http.StatusOK, "")
	if got := strings.Join(order(), ""); got != "X" {
		t.Fatalf("order after losing access = %s", got)
	}
	if res, err := retentionCycle(t, h); err != nil || res.FavouritesRemoved != 1 {
		t.Errorf("retention = %+v, %v, want one favourite removed", res, err)
	}
}

// A parent the caller cannot read is never named: parentId is null in the
// article, the tree and the favourites; an afterId the caller cannot read is
// answered as one that does not exist; a locked article keeps its title
// unless the request unlocks it.
func TestArticles_HiddenParentAfterAndLockedTitle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	p := create(t, a, "", "private", "Mine")
	s := create(t, a, p.ID, "", "Shared")
	expectStatus(t, "share", setAccess(a, s.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "read"}, false), http.StatusOK, "")

	if _, got := get(t, b, s.ID); got.ParentID != nil {
		t.Errorf("B's article names the hidden parent %v", *got.ParentID)
	}
	if _, got := get(t, a, s.ID); got.ParentID == nil || *got.ParentID != p.ID {
		t.Errorf("A's article does not name its parent")
	}
	r := b.c.Do(http.MethodGet, "/api/v1/knowledge/tree", nil)
	if strings.Contains(string(r.Body), p.ID) {
		t.Errorf("B's tree names the hidden parent: %s", r.Body)
	}
	expectStatus(t, "favourite", b.c.Do(http.MethodPut, "/api/v1/knowledge/favourites/"+s.ID, nil), http.StatusNoContent, "")
	r = b.c.Do(http.MethodGet, "/api/v1/knowledge/favourites", nil)
	if strings.Contains(string(r.Body), p.ID) {
		t.Errorf("B's favourites name the hidden parent: %s", r.Body)
	}

	// B places a root after A's private root: as if it did not exist.
	expectStatus(t, "after an unreadable root", b.c.Do(http.MethodPost, "/api/v1/knowledge/articles", map[string]any{"afterId": p.ID}), http.StatusBadRequest, "after_invalid")

	w := create(t, a, "", "workspace", "Policy")
	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+w.ID, map[string]any{"locked": true}), http.StatusOK, "")
	expectStatus(t, "rename a locked article", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+w.ID, map[string]any{"title": "New"}), http.StatusConflict, "article_locked")
	expectStatus(t, "rename and unlock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+w.ID, map[string]any{"title": "New", "locked": false}), http.StatusOK, "")
}

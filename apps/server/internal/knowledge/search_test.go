package knowledge_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

type hitJSON struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Path    []pathItemJSON `json:"path"`
	Excerpt []struct {
		Text  string `json:"text"`
		Match bool   `json:"match"`
	} `json:"excerpt"`
}

func search(t *testing.T, p person, q string) []hitJSON {
	t.Helper()
	r := p.c.Do(http.MethodGet, "/api/v1/knowledge/search?q="+url.QueryEscape(q), nil)
	expectStatus(t, "search "+q, r, http.StatusOK, "")
	var out struct{ Items []hitJSON }
	r.JSON(&out)
	return out.Items
}

func ids(hits []hitJSON) map[string]bool {
	out := map[string]bool{}
	for _, h := range hits {
		out[h.ID] = true
	}
	return out
}

func retentionCycle(t *testing.T, h *modtest.Harness) (knowledge.RetentionResult, error) {
	t.Helper()
	return knowledge.RetentionCycle(context.Background(), h.Deps())
}

// Search in Norwegian and English at once, the last word as a prefix,
// filtered by access and the trash, with the excerpt's matches and a path
// cut at what the reader may see (K12).
func TestSearch_BothLanguagesAccessAndPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)

	nb := create(t, a, "", "workspace", "Faktura")
	expectStatus(t, "save nb", save(a, nb.ID, "Faktura", "Slik sender du en faktura til kunden. <b>ikke fet</b>", 1), http.StatusOK, "")
	en := create(t, a, "", "workspace", "Billing")
	expectStatus(t, "save en", save(a, en.ID, "Billing", "How we send an invoice to the customer.", 1), http.StatusOK, "")
	p := create(t, a, "", "private", "Hemmelig")
	s := create(t, a, p.ID, "", "Lønn")
	expectStatus(t, "save s", save(a, s.ID, "Lønn", "Lønnskjøring og faktura for lønn.", 1), http.StatusOK, "")
	expectStatus(t, "share s", setAccess(a, s.ID, nil, map[uuid.UUID]string{b.id: "read"}, false), http.StatusOK, "")

	if got := ids(search(t, b, "fakturaer")); !got[nb.ID] || !got[s.ID] || got[en.ID] {
		t.Errorf("fakturaer = %v, want the Norwegian article and the shared one", got)
	}
	if got := ids(search(t, b, "invoices")); !got[en.ID] || got[nb.ID] {
		t.Errorf("invoices = %v, want the English article", got)
	}
	if got := ids(search(t, b, "fakt")); !got[nb.ID] {
		t.Errorf("a prefix = %v, want it matched as one", got)
	}
	if got := ids(search(t, b, "send invoi")); !got[en.ID] || got[nb.ID] {
		t.Errorf("two words, the last a prefix = %v", got)
	}
	if got := ids(search(t, b, "hemmelig")); len(got) != 0 {
		t.Errorf("B finds the private article: %v", got)
	}

	for _, hit := range search(t, b, "faktura") {
		switch hit.ID {
		case s.ID:
			if len(hit.Path) != 0 {
				t.Errorf("B's path to the shared article = %v, want it cut at the private parent", hit.Path)
			}
		case nb.ID:
			var matched, literal bool
			for _, seg := range hit.Excerpt {
				if seg.Match && strings.EqualFold(seg.Text, "faktura") {
					matched = true
				}
				if !seg.Match && strings.Contains(seg.Text, "<b>ikke fet</b>") {
					literal = true
				}
			}
			if !matched || !literal {
				t.Errorf("excerpt = %+v, want faktura matched and the markup as text", hit.Excerpt)
			}
		}
	}
	for _, hit := range search(t, a, "lønn") {
		if hit.ID == s.ID && (len(hit.Path) != 1 || hit.Path[0].ID != p.ID) {
			t.Errorf("A's path = %v, want the private parent", hit.Path)
		}
	}

	expectStatus(t, "trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+nb.ID+"/trash", nil), http.StatusNoContent, "")
	if got := ids(search(t, a, "faktura")); got[nb.ID] {
		t.Error("a trashed article is found")
	}
	expectStatus(t, "an empty query", a.c.Do(http.MethodGet, "/api/v1/knowledge/search?q=%20", nil), http.StatusBadRequest, "")
}

func TestSplitQuery(t *testing.T) {
	for q, want := range map[string][2]string{
		"faktura":      {"", "faktura"},
		"send fakt":    {"send ", "fakt"},
		"send ":        {"send ", ""},
		`"en faktura`:  {`"en faktura`, ""},
		`"en" faktura`: {`"en" `, "faktura"},
		"-faktura":     {"-faktura", ""},
		"e-post":       {"e-post", ""},
		"a or":         {"a or", ""},
		"lønn":         {"", "lønn"},
		"faktura!":     {"faktura!", ""},
	} {
		head, last := knowledge.SplitQuery(q)
		if head != want[0] || last != want[1] {
			t.Errorf("SplitQuery(%q) = %q, %q, want %q, %q", q, head, last, want[0], want[1])
		}
	}
}

// A text over 1 MB saves, is read in pieces of 12 000 characters, and is
// searchable on its first 200 000 characters (K12, K14).
func TestText_LargeArticle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	art := create(t, a, "", "workspace", "Big")
	var b strings.Builder
	b.WriteString("tidligord ")
	for b.Len() < 1_100_000 {
		b.WriteString("fyll ")
	}
	b.WriteString("seintord")
	text := b.String()
	expectStatus(t, "save a large article", save(a, art.ID, "Big", text, 1), http.StatusOK, "")

	if got := ids(search(t, a, "tidligord")); !got[art.ID] {
		t.Error("a word in the first 200 000 characters is not found")
	}
	if got := ids(search(t, a, "seintord")); got[art.ID] {
		t.Error("a word past 200 000 characters is found")
	}

	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+art.ID+"/text?offset=12000", nil)
	expectStatus(t, "text", r, http.StatusOK, "")
	var piece struct {
		Text       string
		NextOffset *int32
	}
	r.JSON(&piece)
	if len([]rune(piece.Text)) != 12000 || piece.NextOffset == nil || *piece.NextOffset != 24000 {
		t.Errorf("piece = %d characters, next %v", len([]rune(piece.Text)), piece.NextOffset)
	}
	r = a.c.Do(http.MethodGet, fmt.Sprintf("/api/v1/knowledge/articles/%s/text?offset=%d", art.ID, len(text)-5), nil)
	expectStatus(t, "the last piece", r, http.StatusOK, "")
	r.JSON(&piece)
	if piece.Text != "ntord" || piece.NextOffset != nil {
		t.Errorf("the last piece = %q, next %v", piece.Text, piece.NextOffset)
	}
}

// Erasure (K17): an erased user's private roots go with their subtrees,
// their memberships and favourites go, and their authorship becomes a
// former user's.
func TestRetention_Erasure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	gone := uuid.New()

	shared := create(t, a, "", "workspace", "Shared")
	expectStatus(t, "save", save(a, shared.ID, "Shared", "text", 1), http.StatusOK, "")
	theirs := create(t, a, "", "private", "Theirs")
	child := create(t, a, theirs.ID, "", "Their child")
	// Make the erased user the only writer of theirs, a reader of shared,
	// the last author of shared, and give them a favourite.
	h.Exec(t, `UPDATE knowledge.article_members SET user_id = $1 WHERE article_id = $2`, gone, theirs.ID)
	h.Exec(t, `INSERT INTO knowledge.article_members (article_id, user_id, permission) VALUES ($1, $2, 'read')`, shared.ID, gone)
	h.Exec(t, `UPDATE knowledge.articles SET updated_by = $1, created_by = $1 WHERE id = $2`, gone, shared.ID)
	h.Exec(t, `UPDATE knowledge.article_versions SET created_by = $1 WHERE article_id = $2`, gone, shared.ID)
	h.Exec(t, `INSERT INTO knowledge.favourites (user_id, article_id, position, created_at) VALUES ($1, $2, 'V', now())`, gone, shared.ID)
	for _, id := range []string{theirs.ID, shared.ID} {
		if err := knowledge.Recompute(context.Background(), h.Pool(), uuid.MustParse(id)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := retentionCycle(t, h)
	if err != nil || res.Erased != 1 {
		t.Fatalf("retention = %+v, %v, want one erased user", res, err)
	}
	for _, id := range []string{theirs.ID, child.ID} {
		if n := h.Count(t, `SELECT count(*) FROM knowledge.articles WHERE id = $1`, id); n != 0 {
			t.Errorf("the erased user's private article %s is kept", id)
		}
	}
	for _, q := range []string{
		`SELECT count(*) FROM knowledge.article_members WHERE user_id = $1`,
		`SELECT count(*) FROM knowledge.article_access WHERE user_id = $1`,
		`SELECT count(*) FROM knowledge.favourites WHERE user_id = $1`,
		`SELECT count(*) FROM knowledge.articles WHERE created_by = $1 OR updated_by = $1`,
		`SELECT count(*) FROM knowledge.article_versions WHERE created_by = $1`,
	} {
		if n := h.Count(t, q, gone); n != 0 {
			t.Errorf("%s = %d", q, n)
		}
	}
	if r, got := get(t, a, shared.ID); r.Status != http.StatusOK || got.UpdatedBy != nil {
		t.Errorf("the shared article after the erasure: %d %s, want it kept by a former user", r.Status, r.Body)
	}
}

// The retention purges trash older than 30 days and prunes versions older
// than 365 days beyond the newest 20 (K9, K10).
func TestRetention_TrashAndVersions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	old := create(t, a, "", "workspace", "Old")
	expectStatus(t, "trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+old.ID+"/trash", nil), http.StatusNoContent, "")
	art := create(t, a, "", "workspace", "History")
	// 25 versions, alternating authors so none coalesce.
	v := int32(1)
	for i := range 24 {
		who := a
		if i%2 == 0 {
			who = b
		}
		expectStatus(t, "save", save(who, art.ID, "History", fmt.Sprint(i), v), http.StatusOK, "")
		v++
	}
	h.Advance(31 * 24 * time.Hour)
	res, err := retentionCycle(t, h)
	if err != nil || res.TrashPurged != 1 || res.VersionsPruned != 0 {
		t.Fatalf("after 31 days = %+v, %v", res, err)
	}
	h.Exec(t, `UPDATE knowledge.article_versions SET created_at = created_at - interval '400 days' WHERE article_id = $1`, art.ID)
	res, err = retentionCycle(t, h)
	if err != nil || res.VersionsPruned != 5 {
		t.Fatalf("after a year = %+v, %v, want 5 versions pruned", res, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_versions WHERE article_id = $1`, art.ID); n != 20 {
		t.Errorf("%d versions kept, want 20", n)
	}
}

// The retention holds one connection: on a pool of one it finishes.
func TestRetention_NeedsOneConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(1))
	a := signIn(t, h)
	create(t, a, "", "workspace", "x")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := knowledge.RetentionCycle(ctx, h.Deps()); err != nil {
		t.Fatalf("a cycle on a pool of one: %v", err)
	}
}

// Every write is heard on its topics; a content save that keeps the title
// does not make everyone refetch the sidebar (K13).
func TestLive_WritesAreHeard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	l := listenLive(t, h)
	a := signIn(t, h)
	art := create(t, a, "", "workspace", "Live")
	l.expect(t, treeEvent)

	expectStatus(t, "save", save(a, art.ID, "Live", "text", 1), http.StatusOK, "")
	heard := l.expect(t, articleEvent(art.ID, "content.changed"))
	heard = append(heard, l.drain(300*time.Millisecond)...)
	for _, e := range heard {
		if e == treeEvent {
			t.Error("a content save that kept the title sent tree.changed")
		}
	}
	expectStatus(t, "rename by saving", save(a, art.ID, "Live 2", "text", 2), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "content.changed"), articleEvent(art.ID, "meta.changed"), treeEvent)
	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": true}), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "meta.changed"), treeEvent)
	expectStatus(t, "access", setAccess(a, art.ID, "read", map[uuid.UUID]string{a.id: "write"}, false), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "meta.changed"), treeEvent)
	expectStatus(t, "move", move(a, art.ID, nil, nil, false), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "meta.changed"), treeEvent)
	expectStatus(t, "trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/trash", nil), http.StatusNoContent, "")
	l.expect(t, articleEvent(art.ID, "trashed"), treeEvent)
	expectStatus(t, "restore", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/restore", nil), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "meta.changed"), treeEvent)
	// A refused write rolls its notification back.
	expectStatus(t, "unlock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": false}), http.StatusOK, "")
	l.expect(t, articleEvent(art.ID, "meta.changed"), treeEvent)
	expectStatus(t, "a stale save", save(a, art.ID, "x", "y", 1), http.StatusConflict, "version_conflict")
	for _, e := range l.drain(300 * time.Millisecond) {
		t.Errorf("a refused save was heard: %v", e)
	}
}

// Trashing a group tells every article in it on its own topic (K13).
func TestLive_TrashTellsTheWholeGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	top := create(t, a, "", "workspace", "Top")
	mid := create(t, a, top.ID, "", "Mid")
	leaf := create(t, a, mid.ID, "", "Leaf")
	l := listenLive(t, h)
	expectStatus(t, "trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+top.ID+"/trash", nil), http.StatusNoContent, "")
	l.expect(t, articleEvent(top.ID, "trashed"), articleEvent(mid.ID, "trashed"), articleEvent(leaf.ID, "trashed"), treeEvent)
}

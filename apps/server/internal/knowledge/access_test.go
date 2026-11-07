package knowledge_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// refArticle is an article as the reference computation reads it.
type refArticle struct {
	parent   *uuid.UUID
	internal *string
	hidden   bool
}

// checkMaterialised compares the materialised access (K5) with a recursive
// reference computed from the articles and their members alone: the nearest
// internal_permission and member row for each person on the path — member
// rows stopping at the nearest article with its own default, which is
// detached — hidden when anything on the path is hidden (K16).
func checkMaterialised(t *testing.T, h *modtest.Harness, what string) {
	t.Helper()
	ctx := context.Background()
	articles := map[uuid.UUID]refArticle{}
	rows, err := h.Pool().Query(ctx, `SELECT id, parent_id, internal_permission, ai_hidden FROM knowledge.articles`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var a refArticle
		if err := rows.Scan(&id, &a.parent, &a.internal, &a.hidden); err != nil {
			t.Fatal(err)
		}
		articles[id] = a
	}
	rows.Close()
	members := map[uuid.UUID]map[uuid.UUID]string{}
	rows, err = h.Pool().Query(ctx, `SELECT article_id, user_id, permission FROM knowledge.article_members`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, u uuid.UUID
		var p string
		if err := rows.Scan(&a, &u, &p); err != nil {
			t.Fatal(err)
		}
		if members[a] == nil {
			members[a] = map[uuid.UUID]string{}
		}
		members[a][u] = p
	}
	rows.Close()

	type def struct {
		permission string
		hidden     bool
	}
	wantDefault := map[uuid.UUID]def{}
	wantAccess := map[[2]uuid.UUID]string{}
	for id := range articles {
		d := def{}
		seen := map[uuid.UUID]bool{}
		detached := false
		for cur := &id; cur != nil; cur = articles[*cur].parent {
			a := articles[*cur]
			if !detached {
				for u, p := range members[*cur] {
					if !seen[u] {
						seen[u] = true
						wantAccess[[2]uuid.UUID{id, u}] = p
					}
				}
			}
			if a.internal != nil {
				if d.permission == "" {
					d.permission = *a.internal
				}
				detached = true
			}
			d.hidden = d.hidden || a.hidden
		}
		wantDefault[id] = d
	}

	gotDefault := map[uuid.UUID]def{}
	rows, err = h.Pool().Query(ctx, `SELECT article_id, permission, ai_hidden FROM knowledge.article_access_default`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var d def
		if err := rows.Scan(&id, &d.permission, &d.hidden); err != nil {
			t.Fatal(err)
		}
		gotDefault[id] = d
	}
	rows.Close()
	gotAccess := map[[2]uuid.UUID]string{}
	rows, err = h.Pool().Query(ctx, `SELECT article_id, user_id, permission FROM knowledge.article_access`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, u uuid.UUID
		var p string
		if err := rows.Scan(&a, &u, &p); err != nil {
			t.Fatal(err)
		}
		gotAccess[[2]uuid.UUID{a, u}] = p
	}
	rows.Close()

	for id, w := range wantDefault {
		if g, ok := gotDefault[id]; !ok || g != w {
			t.Fatalf("%s: the default of %s is %+v, the reference %+v", what, id, g, w)
		}
	}
	if len(gotDefault) != len(wantDefault) {
		t.Fatalf("%s: %d defaults materialised for %d articles", what, len(gotDefault), len(wantDefault))
	}
	for k, w := range wantAccess {
		if g := gotAccess[k]; g != w {
			t.Fatalf("%s: the access of %s on %s is %q, the reference %q", what, k[1], k[0], g, w)
		}
	}
	if len(gotAccess) != len(wantAccess) {
		t.Fatalf("%s: %d access rows materialised, the reference has %d", what, len(gotAccess), len(wantAccess))
	}
}

// The materialised access equals a recursive reference over random trees,
// built and then changed one article at a time, each change recomputing only
// that article's subtree as the writes do (K5).
func TestAccess_MaterialisedMatchesReferenceOverRandomTrees(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	levels := []string{"write", "read", "none"}
	users := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}

	for seed := range uint64(4) {
		r := rand.New(rand.NewPCG(seed, 7))
		h.Exec(t, `DELETE FROM knowledge.articles`)
		var ids []uuid.UUID
		parentOf := map[uuid.UUID]*uuid.UUID{}
		for i := range 40 {
			id := uuid.New()
			var parent *uuid.UUID
			if i > 0 && r.IntN(5) != 0 {
				p := ids[r.IntN(len(ids))]
				parent = &p
			}
			var internal *string
			if parent == nil || r.IntN(3) == 0 {
				l := levels[r.IntN(3)]
				internal = &l
			}
			h.Exec(t, `INSERT INTO knowledge.articles (id, parent_id, position, content, internal_permission, ai_hidden, created_at, updated_at)
				VALUES ($1, $2, 'V', '{"type":"doc"}', $3, $4, now(), now())`, id, parent, internal, r.IntN(6) == 0)
			for _, u := range users {
				if r.IntN(6) == 0 {
					h.Exec(t, `INSERT INTO knowledge.article_members (article_id, user_id, permission) VALUES ($1, $2, $3)`, id, u, levels[r.IntN(3)])
				}
			}
			ids = append(ids, id)
			parentOf[id] = parent
			// Each article is recomputed as its create does.
			if err := knowledge.Recompute(ctx, h.Pool(), id); err != nil {
				t.Fatal(err)
			}
		}
		checkMaterialised(t, h, fmt.Sprintf("seed %d, built", seed))

		isDescendant := func(of, maybe uuid.UUID) bool {
			for cur := &maybe; cur != nil; cur = parentOf[*cur] {
				if *cur == of {
					return true
				}
			}
			return false
		}
		for step := range 40 {
			id := ids[r.IntN(len(ids))]
			switch r.IntN(4) {
			case 0: // a default
				var internal *string
				if parentOf[id] == nil || r.IntN(2) == 0 {
					l := levels[r.IntN(3)]
					internal = &l
				}
				h.Exec(t, `UPDATE knowledge.articles SET internal_permission = $2 WHERE id = $1`, id, internal)
			case 1: // a member
				u := users[r.IntN(len(users))]
				if r.IntN(3) == 0 {
					h.Exec(t, `DELETE FROM knowledge.article_members WHERE article_id = $1 AND user_id = $2`, id, u)
				} else {
					h.Exec(t, `INSERT INTO knowledge.article_members (article_id, user_id, permission) VALUES ($1, $2, $3)
						ON CONFLICT (article_id, user_id) DO UPDATE SET permission = EXCLUDED.permission`, id, u, levels[r.IntN(3)])
				}
			case 2: // ai_hidden
				h.Exec(t, `UPDATE knowledge.articles SET ai_hidden = NOT ai_hidden WHERE id = $1`, id)
			case 3: // a move under an article that is not below it, or to the root
				target := ids[r.IntN(len(ids))]
				if isDescendant(id, target) {
					l := levels[r.IntN(3)]
					h.Exec(t, `UPDATE knowledge.articles SET parent_id = NULL, internal_permission = coalesce(internal_permission, $2) WHERE id = $1`, id, l)
					parentOf[id] = nil
				} else {
					h.Exec(t, `UPDATE knowledge.articles SET parent_id = $2 WHERE id = $1`, id, target)
					parentOf[id] = &target
				}
			}
			if err := knowledge.Recompute(ctx, h.Pool(), id); err != nil {
				t.Fatal(err)
			}
			checkMaterialised(t, h, fmt.Sprintf("seed %d, step %d", seed, step))
		}
	}
}

// Two crossing moves — A under B and B under A at once — never make a
// cycle: the tree's lock serialises them, and the second sees the first and
// is refused (K5).
func TestAccess_CrossingMovesNeverMakeACycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := signIn(t, h)
	for i := range 8 {
		a := create(t, p, "", "workspace", fmt.Sprint("A", i))
		b := create(t, p, "", "workspace", fmt.Sprint("B", i))
		statuses := make([]int, 2)
		var wg sync.WaitGroup
		for j, pair := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses[j] = move(p, pair[0], pair[1], nil, false).Status
			}()
		}
		wg.Wait()
		ok, refused := 0, 0
		for _, s := range statuses {
			switch s {
			case http.StatusOK:
				ok++
			case http.StatusBadRequest:
				refused++
			}
		}
		if ok != 1 || refused != 1 {
			t.Fatalf("round %d: statuses %v, want one move and one move_into_descendant", i, statuses)
		}
		if n := h.Count(t, `SELECT count(*) FROM knowledge.articles WHERE id IN ($1, $2) AND parent_id IS NULL`, a.ID, b.ID); n != 1 {
			t.Fatalf("round %d: %d roots of the two, want one", i, n)
		}
	}
	checkMaterialised(t, h, "after the crossing moves")
}

// An ancestor's change racing a descendant's member change: whichever
// commits last recomputes from what the other committed, so the
// materialised access matches the reference (K5).
func TestAccess_AncestorAndDescendantChangesRace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := signIn(t, h)
	q := signIn(t, h)
	root := create(t, p, "", "workspace", "Root")
	mid := create(t, p, root.ID, "", "Mid")
	create(t, p, mid.ID, "", "Leaf")
	levels := []string{"write", "read"}
	for i := range 10 {
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			statuses[0] = setAccess(p, root.ID, levels[i%2], map[uuid.UUID]string{p.id: "write", q.id: levels[(i+1)%2]}, true).Status
		}()
		go func() {
			defer wg.Done()
			members := map[uuid.UUID]string{}
			if i%2 == 0 {
				members[q.id] = "none"
			}
			statuses[1] = setAccess(p, mid.ID, nil, members, true).Status
		}()
		wg.Wait()
		if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
			t.Fatalf("round %d: statuses %v", i, statuses)
		}
		checkMaterialised(t, h, fmt.Sprintf("round %d", i))
	}
}

// An article with its own default is detached: members of its ancestors
// stop at it. A private child under a workspace page therefore cannot be
// opened by adding oneself as a member of the page — by a writer of the page
// or by a manager (knowledge design K4, the review's access decision).
func TestAccess_OwnDefaultDetachesFromAncestorMembers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	m := signIn(t, h, "knowledge:manage")
	w := create(t, a, "", "workspace", "Workspace page")
	c := create(t, a, w.ID, "", "Private child")
	expectStatus(t, "make the child private", setAccess(a, c.ID, "none", map[uuid.UUID]string{a.id: "write"}, true), http.StatusOK, "")

	expectStatus(t, "B makes themself a member of the page", setAccess(b, w.ID, "write", map[uuid.UUID]string{b.id: "write"}, false), http.StatusOK, "")
	expectStatus(t, "the manager makes themself a member of the page", setAccess(m, w.ID, "write", map[uuid.UUID]string{b.id: "write", m.id: "write"}, false), http.StatusOK, "")
	for _, who := range []person{b, m} {
		if r, _ := get(t, who, c.ID); r.Status != http.StatusNotFound {
			t.Errorf("a member of the page opens the private child: %d %s", r.Status, r.Body)
		}
	}
	r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+c.ID+"/access", nil)
	expectStatus(t, "the child's access", r, http.StatusOK, "")
	var acc struct {
		Members []struct {
			UserID    string `json:"userId"`
			Inherited bool
		}
	}
	r.JSON(&acc)
	if len(acc.Members) != 1 || acc.Members[0].UserID != a.id.String() || acc.Members[0].Inherited {
		t.Errorf("the child's members = %s, want only its own writer", r.Body)
	}
	// A child that inherits its default still inherits the page's members.
	open := create(t, a, w.ID, "", "Open child")
	r = a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+open.ID+"/access", nil)
	r.JSON(&acc)
	if len(acc.Members) != 2 || !acc.Members[0].Inherited {
		t.Errorf("an inheriting child's members = %s, want the page's two, inherited", r.Body)
	}
	checkMaterialised(t, h, "after the detach scenario")
}

// last_writer covers the subtree: a change that leaves an article below
// with its own default other than write without a writer is refused.
func TestAccess_LastWriterCoversTheSubtree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	w := create(t, a, "", "workspace", "Page")
	c := create(t, a, w.ID, "", "Child")
	// The child keeps its default from the page, and its only writer is A,
	// through the page's member row.
	expectStatus(t, "the page: read, A writes", setAccess(a, w.ID, "read", map[uuid.UUID]string{a.id: "write"}, false), http.StatusOK, "")
	h.Exec(t, `UPDATE knowledge.articles SET internal_permission = 'read' WHERE id = $1`, c.ID)
	if err := knowledge.Recompute(context.Background(), h.Pool(), uuid.MustParse(c.ID)); err != nil {
		t.Fatal(err)
	}
	// Detached, the child now has no writer: any access change above it is
	// refused until it has one.
	expectStatus(t, "a change above an orphan", setAccess(a, w.ID, "read", map[uuid.UUID]string{a.id: "write"}, false), http.StatusBadRequest, "last_writer")
}

// A manager's committed change that takes the article out of their sight
// answers what they may still know, not 404.
func TestAccess_ManagerChangeOutOfSight(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	b := signIn(t, h)
	m := signIn(t, h, "knowledge:manage")
	x := create(t, a, "", "private", "Shared by two")
	expectStatus(t, "two writers", setAccess(a, x.ID, "none", map[uuid.UUID]string{a.id: "write", b.id: "write"}, false), http.StatusOK, "")
	r := setAccess(m, x.ID, "none", map[uuid.UUID]string{a.id: "write"}, true)
	expectStatus(t, "the manager leaves one writer", r, http.StatusOK, "")
	var acc struct {
		EffectivePermission string
		CanManage           bool
		Members             []any
	}
	r.JSON(&acc)
	if acc.EffectivePermission != "none" || acc.CanManage || len(acc.Members) != 0 {
		t.Errorf("the answer = %s, want a minimal body", r.Body)
	}
}

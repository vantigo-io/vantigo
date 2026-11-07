package knowledge

// Articles and the tree (knowledge design K3, K8, K9, K11a, K14, K16).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

const (
	// maxTitle and maxIcon are the title's and the icon's lengths, in
	// characters.
	maxTitle = 200
	maxIcon  = 16
	// coalesceWindow is how soon after their previous save a person's next
	// one replaces that version rather than adding one (K9).
	coalesceWindow = 10 * time.Minute
	// textPiece is how many characters getKnowledgeArticleText answers at a
	// time (K14).
	textPiece = 12000
)

// emptyDocument is a new article's document when none is given.
var emptyDocument = []byte(`{"type":"doc","content":[{"type":"paragraph"}]}`)

// document is a validated document: its JSON, as stored, and its text.
type document struct {
	raw  []byte
	text string
	refs doc.Refs
}

// parseDocument validates a request's document against the allowlist (K8):
// 413 over 2 MiB, 400 invalid_document with the JSON path of what was
// refused. It answers the document in its stored form (doc.Normalise): no
// article link's label, every id lower-case.
func parseDocument(field string, d gen.KnowledgeDocument) (document, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any(d)); err != nil {
		return document{}, invalid(field, codeInvalidDocument, "The document is not JSON.")
	}
	raw, n, err := doc.Canonical(bytes.TrimRight(buf.Bytes(), "\n"))
	if errors.Is(err, doc.ErrTooLarge) {
		return document{}, tooLarge()
	}
	var bad *doc.InvalidError
	if errors.As(err, &bad) {
		f := fieldErrors{code: codeInvalidDocument}
		f.add(field, bad.Reason)
		p := problem(400, codeInvalidDocument, "Invalid document", "")
		p.Errors = &f.errs
		path := bad.Path
		p.Path = &path
		return document{}, &refusal{status: 400, problem: p}
	}
	if err != nil {
		return document{}, fmt.Errorf("knowledge: parse a document: %w", err)
	}
	return document{raw: raw, text: doc.Text(n), refs: doc.References(n)}, nil
}

// storedRefs is what a stored document refers to; one that no longer parses
// refers to nothing.
func storedRefs(raw []byte) doc.Refs {
	n, err := doc.Parse(raw)
	if err != nil {
		return doc.Refs{}
	}
	return doc.References(n)
}

// storedDocument is a stored document as the contract carries it.
func storedDocument(raw []byte) (gen.KnowledgeDocument, error) {
	out := gen.KnowledgeDocument{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("knowledge: read a stored document: %w", err)
	}
	return out, nil
}

// checkTitle and checkIcon record a title's and an icon's field errors.
func checkTitle(f *fieldErrors, title string) {
	if utf8.RuneCountInString(title) > maxTitle {
		f.add("title", fmt.Sprintf("At most %d characters.", maxTitle))
	}
}

func checkIcon(f *fieldErrors, icon string) {
	if utf8.RuneCountInString(icon) > maxIcon {
		f.add("icon", fmt.Sprintf("At most %d characters.", maxIcon))
	}
}

// iconOf is a request's icon as stored: empty is none.
func iconOf(icon string) *string {
	if icon == "" {
		return nil
	}
	return &icon
}

// positionAfter is the key for an article placed after the sibling after
// under parent (first when after is nil), leaving exclude — the article being
// moved — out of its siblings (K3). after must be a sibling that is not
// trashed and user can read (400 after_invalid, as for one that does not
// exist).
func positionAfter(ctx context.Context, q *store.Queries, user uuid.UUID, parent, after *uuid.UUID, exclude uuid.UUID) (string, error) {
	prev := ""
	if after != nil {
		if *after == exclude {
			return "", invalid("afterId", codeAfterInvalid, "An article cannot come after itself.")
		}
		pos, err := q.SiblingPosition(ctx, store.SiblingPositionParams{After: *after, ParentID: parent, UserID: user})
		if err != nil {
			return "", fmt.Errorf("knowledge: read a sibling's position: %w", err)
		}
		if pos == "" {
			return "", invalid("afterId", codeAfterInvalid, "afterId is not an article under that parent.")
		}
		prev = pos
	}
	next, err := q.NextSiblingPosition(ctx, store.NextSiblingPositionParams{ParentID: parent, Exclude: exclude, After: prev})
	if err != nil {
		return "", fmt.Errorf("knowledge: read the next sibling's position: %w", err)
	}
	key, err := keyBetween(prev, next)
	if err != nil {
		return "", fmt.Errorf("knowledge: a key between %q and %q: %w", prev, next, err)
	}
	return key, nil
}

// lastPosition is a key after every root (exclude left out).
func lastPosition(ctx context.Context, q *store.Queries, parent *uuid.UUID, exclude uuid.UUID) (string, error) {
	last, err := q.LastSiblingPosition(ctx, store.LastSiblingPositionParams{ParentID: parent, Exclude: exclude})
	if err != nil {
		return "", fmt.Errorf("knowledge: read the last position: %w", err)
	}
	return keyBetween(last, "")
}

// saveVersion writes a save over the article and records it in the history
// (K9): when coalesce is set and the person's own previous save is less than
// 10 minutes old, it replaces that version; otherwise it adds one. It
// answers the new version.
func saveVersion(ctx context.Context, q *store.Queries, id, user uuid.UUID, title string, d document, now time.Time, coalesce bool) (int32, error) {
	version, err := q.SaveContent(ctx, store.SaveContentParams{
		ID: id, Title: title, Content: d.raw, ContentText: d.text, UserID: user, Now: now,
	})
	if err != nil {
		return 0, fmt.Errorf("knowledge: save article %s: %w", id, err)
	}
	if coalesce {
		latest, err := q.LatestVersion(ctx, id)
		if err != nil && !noRows(err) {
			return 0, fmt.Errorf("knowledge: read the latest version of %s: %w", id, err)
		}
		if err == nil && latest.CreatedBy != nil && *latest.CreatedBy == user && now.Sub(latest.CreatedAt) < coalesceWindow {
			if err := q.ReplaceVersion(ctx, store.ReplaceVersionParams{
				ArticleID: id, Previous: latest.Version, Version: version, Title: title, Content: d.raw, CreatedAt: now,
			}); err != nil {
				return 0, fmt.Errorf("knowledge: coalesce a version of %s: %w", id, err)
			}
			return version, nil
		}
	}
	if err := q.InsertVersion(ctx, store.InsertVersionParams{
		ArticleID: id, Version: version, Title: title, Content: d.raw, CreatedBy: &user, CreatedAt: now,
	}); err != nil {
		return 0, fmt.Errorf("knowledge: record a version of %s: %w", id, err)
	}
	return version, nil
}

// articleOut reads the article as the editor reads it, for user (K3, K12).
func (s *server) articleOut(ctx context.Context, id, user uuid.UUID) (gen.KnowledgeArticle, error) {
	var (
		m       meta
		content store.ArticleContentRow
		path    map[uuid.UUID][]gen.KnowledgePathItem
	)
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		if m, err = readableMeta(ctx, q, id, user); err != nil {
			return err
		}
		if content, err = q.ArticleContent(ctx, id); err != nil {
			return fmt.Errorf("knowledge: read the document of %s: %w", id, err)
		}
		path, err = paths(ctx, q, user, false, []uuid.UUID{id})
		return err
	})
	if err != nil {
		return gen.KnowledgeArticle{}, err
	}
	body, err := storedDocument(content.Content)
	if err != nil {
		return gen.KnowledgeArticle{}, err
	}
	names, err := s.actors(ctx, m.CreatedBy, m.UpdatedBy)
	if err != nil {
		return gen.KnowledgeArticle{}, err
	}
	return gen.KnowledgeArticle{
		Id: m.ID, ParentId: visibleParent(m.ParentID, path[id]), Position: m.Position, Title: m.Title, Icon: m.Icon,
		Content: body, Version: m.Version, Locked: m.Locked,
		AiHidden: m.AiHidden, AiHiddenInherited: m.Hidden && !m.AiHidden,
		Permission: permissionOf(m.Permission),
		Section:    sectionOf(m.DefaultPermission, m.Permission, m.Writers),
		Path:       path[id],
		CreatedBy:  actorOf(names, m.CreatedBy), CreatedAt: m.CreatedAt,
		UpdatedBy: actorOf(names, m.UpdatedBy), UpdatedAt: m.UpdatedAt,
	}, nil
}

// visibleParent is parent when the caller can read it — the path's last
// item — and null otherwise, so a parent they may not see is never named.
func visibleParent(parent *uuid.UUID, path []gen.KnowledgePathItem) *uuid.UUID {
	if parent == nil || len(path) == 0 || path[len(path)-1].Id != *parent {
		return nil
	}
	return parent
}

// CreateKnowledgeArticle creates a child of an article the caller may write,
// inheriting its access, or a root in the workspace or in private (K3, K4).
func (s *server) CreateKnowledgeArticle(ctx context.Context, req gen.CreateKnowledgeArticleRequestObject) (gen.CreateKnowledgeArticleResponseObject, error) {
	fail := func(err error) (gen.CreateKnowledgeArticleResponseObject, error) {
		return answer(ctx, s, err, answers[gen.CreateKnowledgeArticleResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.CreateKnowledgeArticleResponseObject {
				return gen.CreateKnowledgeArticle400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.CreateKnowledgeArticleResponseObject {
				return gen.CreateKnowledgeArticle403ApplicationProblemPlusJSONResponse(p)
			},
			tooLarge: func(p gen.KnowledgeProblem) gen.CreateKnowledgeArticleResponseObject {
				return gen.CreateKnowledgeArticle413ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	body := gen.KnowledgeArticleCreate{}
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	title, icon := "", ""
	if body.Title != nil {
		title = *body.Title
		checkTitle(&f, title)
	}
	if body.Icon != nil {
		icon = *body.Icon
		checkIcon(&f, icon)
	}
	section := gen.KnowledgeRootSectionWorkspace
	if body.Section != nil {
		section = *body.Section
		if section != gen.KnowledgeRootSectionWorkspace && section != gen.KnowledgeRootSectionPrivate {
			f.add("section", "Must be workspace or private.")
		}
	}
	if err := f.err(); err != nil {
		return fail(err)
	}
	d := document{raw: emptyDocument}
	if body.Content != nil {
		var err error
		if d, err = parseDocument("content", *body.Content); err != nil {
			return fail(err)
		}
	}
	user := callerID(ctx)
	id := uuid.New()
	now := s.deps.Clock()

	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		var own *string
		if body.ParentId != nil {
			parent, err := loadMeta(ctx, q, *body.ParentId, user)
			if err != nil && !isNotFound(err) {
				return err
			}
			if err != nil || !readable(parent) {
				return invalid("parentId", codeParentInvalid, "No such article to create under.")
			}
			if parent.Permission != levelWrite {
				return readOnly()
			}
			depth, err := q.ArticleDepth(ctx, parent.ID)
			if err != nil {
				return fmt.Errorf("knowledge: read the depth of %s: %w", parent.ID, err)
			}
			if depth+1 > maxDepth {
				return invalid("parentId", codeTooDeep, fmt.Sprintf("The tree is at most %d levels deep.", maxDepth))
			}
		} else {
			l := levelWrite
			if section == gen.KnowledgeRootSectionPrivate {
				l = levelNone
			}
			own = &l
		}
		position, err := positionAfter(ctx, q, user, body.ParentId, body.AfterId, id)
		if err != nil {
			return err
		}
		if err := q.InsertArticle(ctx, store.InsertArticleParams{
			ID: id, ParentID: body.ParentId, Position: position, Title: title, Icon: iconOf(icon),
			Content: d.raw, ContentText: d.text, InternalPermission: own, UserID: user, Now: now,
		}); err != nil {
			return fmt.Errorf("knowledge: create an article: %w", err)
		}
		if err := q.InsertVersion(ctx, store.InsertVersionParams{
			ArticleID: id, Version: 1, Title: title, Content: d.raw, CreatedBy: &user, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("knowledge: record the first version: %w", err)
		}
		// A new article has no files yet, so its document can name none.
		if err := checkFiles(ctx, q, id, d.refs); err != nil {
			return err
		}
		if err := rewriteLinks(ctx, q, id, d.refs); err != nil {
			return err
		}
		if body.ParentId == nil && section == gen.KnowledgeRootSectionPrivate {
			if err := q.UpsertMember(ctx, store.UpsertMemberParams{ArticleID: id, UserID: user, Permission: levelWrite}); err != nil {
				return fmt.Errorf("knowledge: make the creator a writer: %w", err)
			}
		}
		if err := recompute(ctx, q, id); err != nil {
			return err
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		return fail(err)
	}
	out, err := s.articleOut(ctx, id, user)
	if err != nil {
		return nil, err
	}
	s.notifyMentions(ctx, id, title, user, newMentions(doc.Refs{}, d.refs, user))
	return gen.CreateKnowledgeArticle201JSONResponse(out), nil
}

// isNotFound reports whether err is the bare 404.
func isNotFound(err error) bool {
	var r *refusal
	return errors.As(err, &r) && r.status == 404
}

// GetKnowledgeArticle answers the article with its document (K3).
func (s *server) GetKnowledgeArticle(ctx context.Context, req gen.GetKnowledgeArticleRequestObject) (gen.GetKnowledgeArticleResponseObject, error) {
	out, err := s.articleOut(ctx, req.Id, callerID(ctx))
	if err != nil {
		return answer(ctx, s, err, answers[gen.GetKnowledgeArticleResponseObject]{
			notFound: func() gen.GetKnowledgeArticleResponseObject { return gen.GetKnowledgeArticle404Response{} },
		})
	}
	return gen.GetKnowledgeArticle200JSONResponse(out), nil
}

// UpdateKnowledgeArticle changes an article's own fields: the title, the
// icon, the lock (K11a) and whether it is hidden from the Assistant (K16).
// Any writer may. A new title is saved as a version, as a content save's is,
// so an editor holding the old version meets version_conflict rather than
// writing the old title back.
func (s *server) UpdateKnowledgeArticle(ctx context.Context, req gen.UpdateKnowledgeArticleRequestObject) (gen.UpdateKnowledgeArticleResponseObject, error) {
	fail := func(err error) (gen.UpdateKnowledgeArticleResponseObject, error) {
		return answer(ctx, s, err, answers[gen.UpdateKnowledgeArticleResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleResponseObject {
				return gen.UpdateKnowledgeArticle400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleResponseObject {
				return gen.UpdateKnowledgeArticle403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.UpdateKnowledgeArticleResponseObject { return gen.UpdateKnowledgeArticle404Response{} },
			conflict: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleResponseObject {
				return gen.UpdateKnowledgeArticle409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	body := gen.KnowledgeArticleUpdate{}
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if body.Title != nil {
		checkTitle(&f, *body.Title)
	}
	if body.Icon != nil {
		checkIcon(&f, *body.Icon)
	}
	if err := f.err(); err != nil {
		return fail(err)
	}
	user := callerID(ctx)
	now := s.deps.Clock()

	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		// ai_hidden is materialised down the subtree (K16), so its change is
		// a tree write: the tree's lock comes before the article's row.
		if body.AiHidden != nil {
			if err := lockTree(ctx, q); err != nil {
				return err
			}
		}
		if err := q.LockArticle(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: lock article %s: %w", req.Id, err)
		}
		m, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		titleChanged := body.Title != nil && *body.Title != m.Title
		iconChanged := body.Icon != nil && !equalString(iconOf(*body.Icon), m.Icon)
		lockChanged := body.Locked != nil && *body.Locked != m.Locked
		hiddenChanged := body.AiHidden != nil && *body.AiHidden != m.AiHidden
		if !titleChanged && !iconChanged && !lockChanged && !hiddenChanged {
			return nil
		}
		// A locked article keeps its title unless the same request unlocks it
		// (K11a).
		if titleChanged && m.Locked && (body.Locked == nil || *body.Locked) {
			return locked()
		}
		p := store.UpdateArticleFieldsParams{ID: req.Id, UserID: user, Now: now}
		if iconChanged {
			p.SetIcon, p.Icon = true, iconOf(*body.Icon)
		}
		if lockChanged {
			p.SetLocked, p.Locked = true, *body.Locked
		}
		if hiddenChanged {
			p.SetAiHidden, p.AiHidden = true, *body.AiHidden
		}
		if err := q.UpdateArticleFields(ctx, p); err != nil {
			return fmt.Errorf("knowledge: change article %s: %w", req.Id, err)
		}
		if titleChanged {
			content, err := q.ArticleContent(ctx, req.Id)
			if err != nil {
				return fmt.Errorf("knowledge: read the document of %s: %w", req.Id, err)
			}
			d := document{raw: content.Content, text: content.ContentText}
			if _, err := saveVersion(ctx, q, req.Id, user, *body.Title, d, now, true); err != nil {
				return err
			}
		}
		if hiddenChanged {
			if err := recompute(ctx, q, req.Id); err != nil {
				return err
			}
		}
		if err := notifyArticle(ctx, tx, req.Id, eventMetaChanged); err != nil {
			return err
		}
		if titleChanged || iconChanged || lockChanged {
			return notifyTree(ctx, tx)
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	out, err := s.articleOut(ctx, req.Id, user)
	if err != nil {
		return fail(err)
	}
	return gen.UpdateKnowledgeArticle200JSONResponse(out), nil
}

func equalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// UpdateKnowledgeArticleContent is the editor's autosave (K9): the title and
// the document over expectedVersion. It takes the article's row lock only,
// never the tree's.
func (s *server) UpdateKnowledgeArticleContent(ctx context.Context, req gen.UpdateKnowledgeArticleContentRequestObject) (gen.UpdateKnowledgeArticleContentResponseObject, error) {
	fail := func(err error) (gen.UpdateKnowledgeArticleContentResponseObject, error) {
		return answer(ctx, s, err, answers[gen.UpdateKnowledgeArticleContentResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleContentResponseObject {
				return gen.UpdateKnowledgeArticleContent400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleContentResponseObject {
				return gen.UpdateKnowledgeArticleContent403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.UpdateKnowledgeArticleContentResponseObject {
				return gen.UpdateKnowledgeArticleContent404Response{}
			},
			conflict: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleContentResponseObject {
				return gen.UpdateKnowledgeArticleContent409ApplicationProblemPlusJSONResponse(p)
			},
			tooLarge: func(p gen.KnowledgeProblem) gen.UpdateKnowledgeArticleContentResponseObject {
				return gen.UpdateKnowledgeArticleContent413ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if req.Body == nil {
		return fail(invalid("body", "", "A body is required."))
	}
	body := *req.Body
	var f fieldErrors
	checkTitle(&f, body.Title)
	if body.ExpectedVersion < 1 {
		f.add("expectedVersion", "Must be at least 1.")
	}
	if err := f.err(); err != nil {
		return fail(err)
	}
	d, err := parseDocument("content", body.Content)
	if err != nil {
		return fail(err)
	}
	user := callerID(ctx)
	now := s.deps.Clock()
	var (
		version   int32
		mentioned []uuid.UUID
	)
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := q.LockArticle(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: lock article %s: %w", req.Id, err)
		}
		m, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if m.Locked {
			return locked()
		}
		if m.Version != body.ExpectedVersion {
			return versionConflict(m.Version, m.UpdatedAt, m.UpdatedBy)
		}
		if err := checkFiles(ctx, q, req.Id, d.refs); err != nil {
			return err
		}
		previous, err := q.ArticleContent(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("knowledge: read the document of %s: %w", req.Id, err)
		}
		mentioned = newMentions(storedRefs(previous.Content), d.refs, user)
		if version, err = saveVersion(ctx, q, req.Id, user, body.Title, d, now, true); err != nil {
			return err
		}
		if err := rewriteLinks(ctx, q, req.Id, d.refs); err != nil {
			return err
		}
		if err := notifyArticle(ctx, tx, req.Id, eventContentChanged); err != nil {
			return err
		}
		if body.Title != m.Title {
			if err := notifyArticle(ctx, tx, req.Id, eventMetaChanged); err != nil {
				return err
			}
			return notifyTree(ctx, tx)
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	s.notifyMentions(ctx, req.Id, body.Title, user, mentioned)
	return gen.UpdateKnowledgeArticleContent200JSONResponse{
		Id: req.Id, Version: version, UpdatedAt: now, UpdatedBy: s.actor(ctx, &user),
	}, nil
}

// MoveKnowledgeArticle moves an article under a new parent, after a sibling
// (K3), and recomputes its subtree's access in the same transaction (K5). A
// move under the article itself or below it is move_into_descendant, one
// that would make the tree deeper than 10 levels too_deep. An article that
// becomes a root keeps who can read it: its effective default becomes its
// own, and its inherited members its own rows — unless section says where it
// goes. A move between private and the workspace needs confirmVisibility.
func (s *server) MoveKnowledgeArticle(ctx context.Context, req gen.MoveKnowledgeArticleRequestObject) (gen.MoveKnowledgeArticleResponseObject, error) {
	fail := func(err error) (gen.MoveKnowledgeArticleResponseObject, error) {
		return answer(ctx, s, err, answers[gen.MoveKnowledgeArticleResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.MoveKnowledgeArticleResponseObject {
				return gen.MoveKnowledgeArticle400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.MoveKnowledgeArticleResponseObject {
				return gen.MoveKnowledgeArticle403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.MoveKnowledgeArticleResponseObject { return gen.MoveKnowledgeArticle404Response{} },
			conflict: func(p gen.KnowledgeProblem) gen.MoveKnowledgeArticleResponseObject {
				return gen.MoveKnowledgeArticle409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if req.Body == nil {
		return fail(invalid("body", "", "A body is required."))
	}
	body := *req.Body
	if body.Section != nil && *body.Section != gen.KnowledgeRootSectionWorkspace && *body.Section != gen.KnowledgeRootSectionPrivate {
		return fail(invalid("section", "", "Must be workspace or private."))
	}
	user := callerID(ctx)
	confirm := body.ConfirmVisibility != nil && *body.ConfirmVisibility
	var node gen.KnowledgeTreeNode
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		before, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		height, err := q.SubtreeHeight(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("knowledge: read the height of %s: %w", req.Id, err)
		}
		own := before.InternalPermission
		if body.ParentId != nil {
			parent, err := loadMeta(ctx, q, *body.ParentId, user)
			if err != nil && !isNotFound(err) {
				return err
			}
			if err != nil || !readable(parent) {
				return invalid("parentId", codeParentInvalid, "No such article to move under.")
			}
			cycle, err := q.IsAncestorOrSelf(ctx, store.IsAncestorOrSelfParams{Of: parent.ID, Ancestor: req.Id})
			if err != nil {
				return fmt.Errorf("knowledge: check a move for a cycle: %w", err)
			}
			if cycle {
				return invalid("parentId", codeMoveIntoDescendant, "An article cannot move under itself or its own descendant.")
			}
			if parent.Permission != levelWrite {
				return readOnly()
			}
			depth, err := q.ArticleDepth(ctx, parent.ID)
			if err != nil {
				return fmt.Errorf("knowledge: read the depth of %s: %w", parent.ID, err)
			}
			if depth+height > maxDepth {
				return invalid("parentId", codeTooDeep, fmt.Sprintf("The tree is at most %d levels deep.", maxDepth))
			}
		} else {
			if height > maxDepth {
				return invalid("parentId", codeTooDeep, fmt.Sprintf("The tree is at most %d levels deep.", maxDepth))
			}
			switch {
			case body.Section != nil && *body.Section == gen.KnowledgeRootSectionWorkspace:
				l := levelWrite
				own = &l
			case body.Section != nil && *body.Section == gen.KnowledgeRootSectionPrivate:
				l := levelNone
				own = &l
				if err := q.CopyAccessToMembers(ctx, req.Id); err != nil {
					return fmt.Errorf("knowledge: keep the members of %s: %w", req.Id, err)
				}
				if err := q.UpsertMember(ctx, store.UpsertMemberParams{ArticleID: req.Id, UserID: user, Permission: levelWrite}); err != nil {
					return fmt.Errorf("knowledge: make the mover a writer: %w", err)
				}
			case before.ParentID != nil:
				l := before.DefaultPermission
				own = &l
				if err := q.CopyAccessToMembers(ctx, req.Id); err != nil {
					return fmt.Errorf("knowledge: keep the members of %s: %w", req.Id, err)
				}
			}
		}
		position, err := positionAfter(ctx, q, user, body.ParentId, body.AfterId, req.Id)
		if err != nil {
			return err
		}
		if err := q.MoveArticle(ctx, store.MoveArticleParams{ID: req.Id, ParentID: body.ParentId, Position: position, InternalPermission: own}); err != nil {
			return fmt.Errorf("knowledge: move article %s: %w", req.Id, err)
		}
		if err := recompute(ctx, q, req.Id); err != nil {
			return err
		}
		after, err := loadMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if err := checkWriters(ctx, q, req.Id); err != nil {
			return err
		}
		if visibilityChanged(before, after) && !confirm {
			return visibilityChange()
		}
		children, err := q.TreeChildren(ctx, store.TreeChildrenParams{UserID: user, ParentID: req.Id})
		if err != nil {
			return fmt.Errorf("knowledge: read the children of %s: %w", req.Id, err)
		}
		node = gen.KnowledgeTreeNode{
			Id: after.ID, ParentId: after.ParentID, Position: after.Position, Title: after.Title, Icon: after.Icon,
			HasChildren: len(children) > 0, Locked: after.Locked,
			Section:    sectionOf(after.DefaultPermission, after.Permission, after.Writers),
			Permission: permissionOf(after.Permission),
		}
		if err := notifyArticle(ctx, tx, req.Id, eventMetaChanged); err != nil {
			return err
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		return fail(err)
	}
	return gen.MoveKnowledgeArticle200JSONResponse(node), nil
}

// GetKnowledgeArticleText answers the article's plain text in pieces of
// 12 000 characters from offset (K14). To the Assistant, an article hidden
// from it is not there (K16).
func (s *server) GetKnowledgeArticleText(ctx context.Context, req gen.GetKnowledgeArticleTextRequestObject) (gen.GetKnowledgeArticleTextResponseObject, error) {
	user := callerID(ctx)
	assistant := viaAssistant(ctx)
	offset := 0
	if req.Params.Offset != nil && *req.Params.Offset > 0 {
		offset = int(*req.Params.Offset)
	}
	var (
		m       meta
		content store.ArticleContentRow
		path    map[uuid.UUID][]gen.KnowledgePathItem
	)
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		if m, err = readableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		if assistant && m.Hidden {
			return notFound()
		}
		if content, err = q.ArticleContent(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: read the text of %s: %w", req.Id, err)
		}
		path, err = paths(ctx, q, user, assistant, []uuid.UUID{req.Id})
		return err
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.GetKnowledgeArticleTextResponseObject]{
			notFound: func() gen.GetKnowledgeArticleTextResponseObject { return gen.GetKnowledgeArticleText404Response{} },
		})
	}
	text := []rune(content.ContentText)
	out := gen.KnowledgeArticleText{Id: m.ID, Title: m.Title, Path: path[req.Id], Text: ""}
	if offset < len(text) {
		end := min(offset+textPiece, len(text))
		out.Text = string(text[offset:end])
		if end < len(text) {
			next := int32(end)
			out.NextOffset = &next
		}
	}
	return gen.GetKnowledgeArticleText200JSONResponse(out), nil
}

// treeNode is a sidebar row as the contract carries it.
func treeNode(id uuid.UUID, parent *uuid.UUID, position, title string, icon *string, isLocked bool,
	defaultPermission, permission string, writers int64, hasChildren bool) gen.KnowledgeTreeNode {
	return gen.KnowledgeTreeNode{
		Id: id, ParentId: parent, Position: position, Title: title, Icon: icon, Locked: isLocked,
		HasChildren: hasChildren,
		Section:     sectionOf(defaultPermission, permission, writers),
		Permission:  permissionOf(permission),
	}
}

// ListKnowledgeTree answers the topmost readable article of every section, or
// the readable children of one article (K4).
func (s *server) ListKnowledgeTree(ctx context.Context, req gen.ListKnowledgeTreeRequestObject) (gen.ListKnowledgeTreeResponseObject, error) {
	user := callerID(ctx)
	items := []gen.KnowledgeTreeNode{}
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		if req.Params.ParentId == nil {
			rows, err := q.TreeRoots(ctx, user)
			if err != nil {
				return fmt.Errorf("knowledge: read the tree: %w", err)
			}
			for _, r := range rows {
				// A topmost article's parent, when it has one, is one the
				// caller cannot read: it is never named.
				items = append(items, treeNode(r.ID, nil, r.Position, r.Title, r.Icon, r.Locked, r.DefaultPermission, r.Permission, r.Writers, r.HasChildren))
			}
			return nil
		}
		if _, err := readableMeta(ctx, q, *req.Params.ParentId, user); err != nil {
			return err
		}
		rows, err := q.TreeChildren(ctx, store.TreeChildrenParams{UserID: user, ParentID: *req.Params.ParentId})
		if err != nil {
			return fmt.Errorf("knowledge: read the children: %w", err)
		}
		for _, r := range rows {
			items = append(items, treeNode(r.ID, r.ParentID, r.Position, r.Title, r.Icon, r.Locked, r.DefaultPermission, r.Permission, r.Writers, r.HasChildren))
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.ListKnowledgeTreeResponseObject]{
			notFound: func() gen.ListKnowledgeTreeResponseObject { return gen.ListKnowledgeTree404Response{} },
		})
	}
	return gen.ListKnowledgeTree200JSONResponse{Items: items}, nil
}

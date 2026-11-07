package knowledge

// Links between articles and mentions of people (knowledge design K20, K21):
// listKnowledgeBacklinks, resolveKnowledgeLinks, the links rewritten on every
// save and the mail a newly mentioned person gets.

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

const (
	// maxResolve is how many link targets one resolveKnowledgeLinks asks
	// about.
	maxResolve = 200
	// mentionThrottle is how often a person is told of mentions in one
	// article at most (K20).
	mentionThrottle = time.Hour
	// noticeTimeout bounds one save's mention notices, all of them together.
	noticeTimeout = 30 * time.Second
)

// uuids parses ids, leaving out what is not a uuid (the allowlist has
// already refused those).
func uuids(ids []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// rewriteLinks replaces from's links with the article links its document
// holds, to the targets that exist (K21).
func rewriteLinks(ctx context.Context, q *store.Queries, from uuid.UUID, refs doc.Refs) error {
	if err := q.DeleteLinksFrom(ctx, from); err != nil {
		return fmt.Errorf("knowledge: clear the links of %s: %w", from, err)
	}
	if len(refs.Articles) == 0 {
		return nil
	}
	if err := q.InsertLinks(ctx, store.InsertLinksParams{FromID: from, ToIds: uuids(refs.Articles)}); err != nil {
		return fmt.Errorf("knowledge: record the links of %s: %w", from, err)
	}
	return nil
}

// ListKnowledgeBacklinks answers the readable articles that link to one the
// caller can read (K21), each with its readable path.
func (s *server) ListKnowledgeBacklinks(ctx context.Context, req gen.ListKnowledgeBacklinksRequestObject) (gen.ListKnowledgeBacklinksResponseObject, error) {
	user := callerID(ctx)
	out := gen.KnowledgeBacklinkList{}
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		if _, err := readableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		rows, err := q.Backlinks(ctx, store.BacklinksParams{UserID: user, ToID: req.Id})
		if err != nil {
			return fmt.Errorf("knowledge: read the backlinks of %s: %w", req.Id, err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		path, err := paths(ctx, q, user, false, ids)
		if err != nil {
			return err
		}
		for _, r := range rows {
			p := path[r.ID]
			// The path ends at the article itself; the link names it already.
			if len(p) > 0 && p[len(p)-1].Id == r.ID {
				p = p[:len(p)-1]
			}
			out = append(out, gen.KnowledgeBacklink{Id: r.ID, Title: r.Title, Icon: r.Icon, Path: p})
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.ListKnowledgeBacklinksResponseObject]{
			notFound: func() gen.ListKnowledgeBacklinksResponseObject { return gen.ListKnowledgeBacklinks404Response{} },
		})
	}
	return gen.ListKnowledgeBacklinks200JSONResponse(out), nil
}

// ResolveKnowledgeLinks answers each asked target as ok with its title and
// icon, no_access, or deleted (K21), in the order asked. An article the
// caller cannot read — trashed included — is no_access, and its title and
// icon are never answered.
func (s *server) ResolveKnowledgeLinks(ctx context.Context, req gen.ResolveKnowledgeLinksRequestObject) (gen.ResolveKnowledgeLinksResponseObject, error) {
	if req.Body == nil {
		return gen.ResolveKnowledgeLinks400ApplicationProblemPlusJSONResponse(problem(400, "", "Invalid request", "A body is required.")), nil
	}
	if len(req.Body.Ids) > maxResolve {
		var f fieldErrors
		f.add("ids", fmt.Sprintf("At most %d.", maxResolve))
		p := problem(400, "", "Invalid request", "")
		p.Errors = &f.errs
		return gen.ResolveKnowledgeLinks400ApplicationProblemPlusJSONResponse(p), nil
	}
	user := callerID(ctx)
	ids := slices.Compact(slices.SortedFunc(slices.Values(req.Body.Ids), func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }))
	var rows []store.ArticlesForRow
	if len(ids) > 0 {
		var err error
		if rows, err = store.New(s.deps.Pool).ArticlesFor(ctx, store.ArticlesForParams{UserID: user, Ids: ids}); err != nil {
			return nil, fmt.Errorf("knowledge: resolve links: %w", err)
		}
	}
	found := make(map[uuid.UUID]store.ArticlesForRow, len(rows))
	for _, r := range rows {
		found[r.ID] = r
	}
	out := make(gen.KnowledgeResolvedLinkList, 0, len(req.Body.Ids))
	for _, id := range req.Body.Ids {
		r, ok := found[id]
		switch {
		case !ok:
			out = append(out, gen.KnowledgeResolvedLink{Id: id, State: "deleted"})
		case !r.Readable:
			out = append(out, gen.KnowledgeResolvedLink{Id: id, State: "no_access"})
		default:
			title := r.Title
			out = append(out, gen.KnowledgeResolvedLink{Id: id, State: "ok", Title: &title, Icon: r.Icon})
		}
	}
	return gen.ResolveKnowledgeLinks200JSONResponse(out), nil
}

// newMentions is the people now mentioned who were not before, the author
// left out (K20).
func newMentions(before, after doc.Refs, author uuid.UUID) []uuid.UUID {
	had := map[string]bool{}
	for _, id := range before.Mentions {
		had[id] = true
	}
	var out []uuid.UUID
	for _, id := range uuids(after.Mentions) {
		if !had[id.String()] && id != author {
			out = append(out, id)
		}
	}
	return out
}

// notifyMentions tells the people newly mentioned in an article, after the
// save committed (K20): only those who may read it — knowledge:articles and
// the article's own access — and each at most once an hour per article.
// Anyone else is skipped silently. A failure is logged and never fails the
// save.
func (s *server) notifyMentions(ctx context.Context, article uuid.UUID, title string, author uuid.UUID, mentioned []uuid.UUID) {
	if len(mentioned) == 0 || s.deps.Users == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), noticeTimeout)
	defer cancel()
	logFail := func(what string, err error) {
		s.deps.Logger.WarnContext(ctx, "knowledge: "+what, "article_id", article.String(), "error", err.Error())
	}
	q := store.New(s.deps.Pool)
	readers, err := q.ReadersAmong(ctx, store.ReadersAmongParams{Users: mentioned, ArticleID: article})
	if err != nil {
		logFail("the mentioned readers could not be read", err)
		return
	}
	if len(readers) == 0 {
		return
	}
	holders, err := s.deps.Users.PermissionHolders(ctx, permArticles)
	if err != nil {
		logFail("the holders of knowledge:articles could not be read", err)
		return
	}
	holds := map[uuid.UUID]bool{}
	for _, h := range holders {
		holds[h.ID] = true
	}
	var allowed []uuid.UUID
	for _, id := range readers {
		if holds[id] {
			allowed = append(allowed, id)
		}
	}
	if len(allowed) == 0 {
		return
	}
	now := s.deps.Clock()
	claimed, err := q.ClaimMentionNotices(ctx, store.ClaimMentionNoticesParams{
		ArticleID: article, Users: allowed, Now: now, Cutoff: now.Add(-mentionThrottle),
	})
	if err != nil {
		logFail("the mention notices could not be recorded", err)
		return
	}
	name := "Someone"
	if a := s.actor(ctx, &author); a != nil {
		name = a.DisplayName
	}
	texts := mentionTexts(name, title, s.appLink("/knowledge/articles/"+article.String()))
	for _, id := range claimed {
		if err := s.deps.Users.Notify(ctx, id, contracts.Notice{Texts: texts}); err != nil {
			s.deps.Logger.WarnContext(ctx, "knowledge: a mention notice could not be sent",
				"article_id", article.String(), "user_id", id.String(), "error", err.Error())
		}
	}
}

// mentionTexts is a mention's notice in English and Norwegian.
func mentionTexts(name, title, link string) map[string]contracts.NoticeText {
	enTitle, nbTitle := title, title
	if title == "" {
		enTitle, nbTitle = "Untitled", "Uten tittel"
	}
	en := contracts.NoticeText{
		Subject: fmt.Sprintf("%s mentioned you in “%s”", name, enTitle),
		Body:    fmt.Sprintf("%s mentioned you in the Knowledge article “%s”.\n", name, enTitle),
	}
	nb := contracts.NoticeText{
		Subject: fmt.Sprintf("%s nevnte deg i «%s»", name, nbTitle),
		Body:    fmt.Sprintf("%s nevnte deg i Kunnskap-artikkelen «%s».\n", name, nbTitle),
	}
	if link != "" {
		en.Body += "\nOpen it: " + link + "\n"
		nb.Body += "\nÅpne den: " + link + "\n"
	}
	return map[string]contracts.NoticeText{"en": en, "nb": nb}
}

// appLink is page in the app, "" when the installation has no origin.
func (s *server) appLink(page string) string {
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		return cfg.AppOrigin + cfg.BasePath + page
	}
	return ""
}

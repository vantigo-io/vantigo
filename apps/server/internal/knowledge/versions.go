package knowledge

// The history (knowledge design K9): one version per save, coalesced; a
// restore saves an old version as a new one.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

// ListKnowledgeArticleVersions answers an article's versions, newest first.
func (s *server) ListKnowledgeArticleVersions(ctx context.Context, req gen.ListKnowledgeArticleVersionsRequestObject) (gen.ListKnowledgeArticleVersionsResponseObject, error) {
	user := callerID(ctx)
	var (
		m    meta
		rows []store.ListVersionsRow
	)
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		var err error
		if m, err = readableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		if rows, err = q.ListVersions(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: read the versions of %s: %w", req.Id, err)
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.ListKnowledgeArticleVersionsResponseObject]{
			notFound: func() gen.ListKnowledgeArticleVersionsResponseObject {
				return gen.ListKnowledgeArticleVersions404Response{}
			},
		})
	}
	ids := make([]*uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].CreatedBy)
	}
	names, err := s.actors(ctx, ids...)
	if err != nil {
		return nil, err
	}
	items := make([]gen.KnowledgeVersionSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, gen.KnowledgeVersionSummary{
			Version: r.Version, Title: r.Title, CreatedBy: actorOf(names, r.CreatedBy), CreatedAt: r.CreatedAt,
			Current: r.Version == m.Version,
		})
	}
	return gen.ListKnowledgeArticleVersions200JSONResponse{Items: items}, nil
}

// GetKnowledgeArticleVersion answers one version with its document.
func (s *server) GetKnowledgeArticleVersion(ctx context.Context, req gen.GetKnowledgeArticleVersionRequestObject) (gen.GetKnowledgeArticleVersionResponseObject, error) {
	user := callerID(ctx)
	var v store.GetVersionRow
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		if _, err := readableMeta(ctx, q, req.Id, user); err != nil {
			return err
		}
		var err error
		v, err = q.GetVersion(ctx, store.GetVersionParams{ArticleID: req.Id, Version: req.Version})
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("knowledge: read version %d of %s: %w", req.Version, req.Id, err)
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.GetKnowledgeArticleVersionResponseObject]{
			notFound: func() gen.GetKnowledgeArticleVersionResponseObject {
				return gen.GetKnowledgeArticleVersion404Response{}
			},
		})
	}
	content, err := storedDocument(v.Content)
	if err != nil {
		return nil, err
	}
	return gen.GetKnowledgeArticleVersion200JSONResponse{
		Version: v.Version, Title: v.Title, Content: content, CreatedBy: s.actor(ctx, v.CreatedBy), CreatedAt: v.CreatedAt,
	}, nil
}

// RestoreKnowledgeArticleVersion saves a version's title and document as a
// new version over expectedVersion; every version before it stays (K9). It
// is never coalesced, and it deletes the article's live log (K32).
func (s *server) RestoreKnowledgeArticleVersion(ctx context.Context, req gen.RestoreKnowledgeArticleVersionRequestObject) (gen.RestoreKnowledgeArticleVersionResponseObject, error) {
	fail := func(err error) (gen.RestoreKnowledgeArticleVersionResponseObject, error) {
		return answer(ctx, s, err, answers[gen.RestoreKnowledgeArticleVersionResponseObject]{
			forbidden: func(p gen.KnowledgeProblem) gen.RestoreKnowledgeArticleVersionResponseObject {
				return gen.RestoreKnowledgeArticleVersion403ApplicationProblemPlusJSONResponse(p)
			},
			notFound: func() gen.RestoreKnowledgeArticleVersionResponseObject {
				return gen.RestoreKnowledgeArticleVersion404Response{}
			},
			conflict: func(p gen.KnowledgeProblem) gen.RestoreKnowledgeArticleVersionResponseObject {
				return gen.RestoreKnowledgeArticleVersion409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if req.Body == nil {
		return fail(notFound())
	}
	user := callerID(ctx)
	now := s.deps.Clock()
	var version int32
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := q.LockArticle(ctx, req.Id); err != nil {
			return fmt.Errorf("knowledge: lock article %s: %w", req.Id, err)
		}
		m, err := writableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		v, err := q.GetVersion(ctx, store.GetVersionParams{ArticleID: req.Id, Version: req.Version})
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("knowledge: read version %d of %s: %w", req.Version, req.Id, err)
		}
		if m.Locked {
			return locked()
		}
		if m.Version != req.Body.ExpectedVersion {
			return versionConflict(m.Version, m.UpdatedAt, m.UpdatedBy)
		}
		// An older version is saved in today's stored form (doc.Normalise).
		raw, n, err := doc.Canonical(v.Content)
		if err != nil {
			return fmt.Errorf("knowledge: read version %d of %s: %w", req.Version, req.Id, err)
		}
		d := document{raw: raw, text: doc.Text(n), refs: doc.References(n)}
		if version, err = saveVersion(ctx, q, req.Id, user, v.Title, d, now, false); err != nil {
			return err
		}
		if err := rewriteLinks(ctx, q, req.Id, d.refs); err != nil {
			return err
		}
		if err := resetLog(ctx, tx, q, req.Id); err != nil {
			return err
		}
		if err := notifyArticle(ctx, tx, req.Id, eventContentChanged); err != nil {
			return err
		}
		if v.Title != m.Title {
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
	return gen.RestoreKnowledgeArticleVersion200JSONResponse{
		Id: req.Id, Version: version, UpdatedAt: now, UpdatedBy: s.actor(ctx, &user),
	}, nil
}

package knowledge

// Search (knowledge design K12, K16): full text in Postgres, in Norwegian and
// English at once, the last word matched as a prefix too so a search finds
// as the person types. Only articles the caller can read are found, never a
// trashed one, and for the Assistant none hidden from it.

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
	searchMaxQuery     = 200
	// The excerpt's match markers: control characters the search takes out
	// of the text before ts_headline puts them in, so nothing in an article
	// can pass for a match (or for markup).
	matchStart = '\x02'
	matchStop  = '\x03'
)

// splitQuery splits q into the part websearch_to_tsquery reads as it is and
// its last word, which is matched as a prefix too. A query that ends in
// anything but a word — a space, a quote, a word inside a phrase or after
// "-" — has no last word.
func splitQuery(q string) (head, last string) {
	rs := []rune(q)
	end := len(rs)
	if end == 0 || !isWordRune(rs[end-1]) {
		return q, ""
	}
	start := end
	for start > 0 && isWordRune(rs[start-1]) {
		start--
	}
	if start > 0 && !unicode.IsSpace(rs[start-1]) {
		return q, ""
	}
	head = string(rs[:start])
	if strings.Count(head, `"`)%2 == 1 {
		return q, ""
	}
	last = string(rs[start:])
	if strings.EqualFold(last, "or") {
		return q, ""
	}
	return head, last
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// excerptOf turns a ts_headline with the match markers into segments.
func excerptOf(headline string) []gen.KnowledgeExcerptSegment {
	segments := []gen.KnowledgeExcerptSegment{}
	var b strings.Builder
	match := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		if n := len(segments); n > 0 && segments[n-1].Match == match {
			segments[n-1].Text += b.String()
		} else {
			segments = append(segments, gen.KnowledgeExcerptSegment{Text: b.String(), Match: match})
		}
		b.Reset()
	}
	for _, r := range headline {
		switch r {
		case matchStart:
			flush()
			match = true
		case matchStop:
			flush()
			match = false
		case '\ue000':
			b.WriteByte('<')
		case '\ue001':
			b.WriteByte('>')
		case '\ue002':
			b.WriteByte('&')
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segments
}

// SearchKnowledge finds articles by their title and text (K12).
func (s *server) SearchKnowledge(ctx context.Context, req gen.SearchKnowledgeRequestObject) (gen.SearchKnowledgeResponseObject, error) {
	var f fieldErrors
	q := strings.TrimLeftFunc(req.Params.Q, unicode.IsSpace)
	if strings.TrimSpace(q) == "" {
		f.add("q", "A search needs at least one word.")
	}
	if utf8.RuneCountInString(req.Params.Q) > searchMaxQuery {
		f.add("q", fmt.Sprintf("At most %d characters.", searchMaxQuery))
	}
	limit := int32(searchDefaultLimit)
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit < 1 || limit > searchMaxLimit {
			f.add("limit", fmt.Sprintf("Must be between 1 and %d.", searchMaxLimit))
		}
	}
	if err := f.err(); err != nil {
		return answer(ctx, s, err, answers[gen.SearchKnowledgeResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.SearchKnowledgeResponseObject {
				return gen.SearchKnowledge400ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	// The markers are the excerpt's own; a query carrying them would only
	// confuse ts_headline's options.
	q = strings.Map(func(r rune) rune {
		if r == matchStart || r == matchStop {
			return ' '
		}
		return r
	}, q)
	head, last := splitQuery(q)
	user := callerID(ctx)
	assistant := viaAI(ctx)

	var (
		rows []store.SearchRow
		path map[uuid.UUID][]gen.KnowledgePathItem
	)
	err := s.withReadTx(ctx, func(qs *store.Queries) error {
		var err error
		rows, err = qs.Search(ctx, store.SearchParams{Head: head, Last: last, UserID: user, Assistant: assistant, MaxHits: limit})
		if err != nil {
			return fmt.Errorf("knowledge: search: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		path, err = paths(ctx, qs, user, assistant, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	items := make([]gen.KnowledgeSearchHit, 0, len(rows))
	for _, r := range rows {
		headline := r.HeadlineNb
		if !strings.ContainsRune(headline, matchStart) && strings.ContainsRune(r.HeadlineEn, matchStart) {
			headline = r.HeadlineEn
		}
		items = append(items, gen.KnowledgeSearchHit{
			Id: r.ID, Title: r.Title, Icon: r.Icon, Path: path[r.ID], Excerpt: excerptOf(headline), UpdatedAt: r.UpdatedAt,
		})
	}
	return gen.SearchKnowledge200JSONResponse{Items: items}, nil
}

package knowledge

// Markdown export (knowledge design K23): one article as a .md, or with its
// subtree — or with files — as a .zip laid out the way Obsidian keeps a
// vault: an article's .md beside a folder of the same name holding its
// children, every file in an assets/ folder at the top.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// assetsFolder is where an export keeps its files.
const assetsFolder = "assets"

// exportDownload names the download.
type exportDownload struct {
	body     gen.ExportKnowledgeArticleResponseObject
	filename string
}

func (r exportDownload) VisitExportKnowledgeArticleResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.filename != "" {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+rfc5987(r.filename))
	}
	return r.body.VisitExportKnowledgeArticleResponse(w)
}

// exportArticle is one article as exported: where its .md goes and its
// document.
type exportArticle struct {
	id    uuid.UUID
	path  string // its .md, from the archive's root
	depth int    // how many folders deep path is
	node  *doc.Node
}

// ExportKnowledgeArticle answers the article as Markdown, or with
// subtree=true the article and the descendants the caller can read as a .zip
// (K23). A link to an article the caller cannot read is "No access", never
// a stored label.
func (s *server) ExportKnowledgeArticle(ctx context.Context, req gen.ExportKnowledgeArticleRequestObject) (gen.ExportKnowledgeArticleResponseObject, error) {
	user := callerID(ctx)
	subtree := req.Params.Subtree != nil && *req.Params.Subtree
	var (
		rows    []store.SubtreeForExportRow
		files   []store.FilesOfArticlesRow
		targets map[string]string
	)
	err := s.withReadTx(ctx, func(q *store.Queries) error {
		m, err := readableMeta(ctx, q, req.Id, user)
		if err != nil {
			return err
		}
		if subtree {
			if rows, err = q.SubtreeForExport(ctx, store.SubtreeForExportParams{Root: req.Id, UserID: user}); err != nil {
				return fmt.Errorf("knowledge: read the subtree of %s: %w", req.Id, err)
			}
		} else {
			content, err := q.ArticleContent(ctx, req.Id)
			if err != nil {
				return fmt.Errorf("knowledge: read the document of %s: %w", req.Id, err)
			}
			rows = []store.SubtreeForExportRow{{ID: m.ID, ParentID: m.ParentID, Title: m.Title, Icon: m.Icon, Content: content.Content}}
		}
		ids := make([]uuid.UUID, 0, len(rows))
		var linked []string
		for _, r := range rows {
			ids = append(ids, r.ID)
			linked = append(linked, storedRefs(r.Content).Articles...)
		}
		if files, err = q.FilesOfArticles(ctx, ids); err != nil {
			return fmt.Errorf("knowledge: read the files: %w", err)
		}
		targets = map[string]string{}
		if len(linked) > 0 {
			found, err := q.ArticlesFor(ctx, store.ArticlesForParams{UserID: user, Ids: uuids(linked)})
			if err != nil {
				return fmt.Errorf("knowledge: read the link targets: %w", err)
			}
			for _, f := range found {
				if f.Readable {
					targets[f.ID.String()] = f.Title
				}
			}
		}
		return nil
	})
	if err != nil {
		return answer(ctx, s, err, answers[gen.ExportKnowledgeArticleResponseObject]{
			notFound: func() gen.ExportKnowledgeArticleResponseObject { return gen.ExportKnowledgeArticle404Response{} },
		})
	}

	// Lay out the archive: every article's .md under its readable parent's
	// folder, siblings' names made unique.
	byID := map[uuid.UUID]*exportArticle{}
	folders := map[uuid.UUID]string{}
	taken := map[string]bool{assetsFolder: true}
	var articles []*exportArticle
	for _, r := range rows {
		n, err := doc.Parse(r.Content)
		if err != nil {
			return nil, fmt.Errorf("knowledge: read the document of %s: %w", r.ID, err)
		}
		dir, depth := "", 0
		if r.ParentID != nil && r.ID != req.Id {
			if parent, ok := byID[*r.ParentID]; ok {
				dir, depth = folders[parent.id]+"/", parent.depth+1
			}
		}
		name := uniqueName(taken, dir, safeName(r.Title))
		a := &exportArticle{id: r.ID, path: dir + name + ".md", depth: depth, node: n}
		folders[r.ID] = dir + name
		byID[r.ID] = a
		articles = append(articles, a)
	}

	// The files the documents use, each at assets/<id>-<name>.
	fileByID := map[string]store.FilesOfArticlesRow{}
	for _, f := range files {
		fileByID[f.ID.String()] = f
	}
	used := map[string]string{}
	var usedOrder []string
	for _, a := range articles {
		for _, id := range doc.References(a.node).Files {
			f, ok := fileByID[id]
			if !ok || f.ArticleID != a.id {
				continue
			}
			if _, done := used[id]; !done {
				used[id] = assetsFolder + "/" + id + "-" + safeName(f.Filename)
				usedOrder = append(usedOrder, id)
			}
		}
	}

	markdown := func(a *exportArticle) string {
		return doc.Markdown(a.node, doc.Exporter{
			Article: func(id string) (string, bool) {
				title, ok := targets[id]
				return title, ok
			},
			File: func(id string) (string, bool) {
				path, ok := used[id]
				if !ok {
					return "", false
				}
				return strings.Repeat("../", a.depth) + path, true
			},
		})
	}

	root := articles[0]
	if !subtree && len(used) == 0 {
		body := []byte(markdown(root))
		return exportDownload{
			body:     gen.ExportKnowledgeArticle200TextmarkdownResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
			filename: strings.TrimPrefix(root.path, "/"),
		}, nil
	}

	// The size is known before a byte is written: the files' sizes from
	// their rows and the documents' JSON, which their Markdown never
	// outgrows by much.
	entries := len(articles) + len(usedOrder)
	var size int64
	for _, r := range rows {
		size += int64(len(r.Content))
	}
	for _, id := range usedOrder {
		size += fileByID[id].Size
	}
	if entries > exportMaxEntries || size > exportMaxBytes {
		return gen.ExportKnowledgeArticle413ApplicationProblemPlusJSONResponse(problem(http.StatusRequestEntityTooLarge, codeExportTooLarge,
			"The export is too large", fmt.Sprintf("An export holds at most %d articles and files, and 1 GiB.", exportMaxEntries))), nil
	}
	if len(usedOrder) > 0 {
		if err := s.objects.ready(); err != nil {
			s.deps.Logger.ErrorContext(ctx, "knowledge: the file store is not available for an export", "article_id", req.Id.String(), "error", err.Error())
			return gen.ExportKnowledgeArticle503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
		}
	}
	streamed := make([]exportFile, 0, len(usedOrder))
	for _, id := range usedOrder {
		f := fileByID[id]
		streamed = append(streamed, exportFile{path: used[id], key: f.ObjectKey, article: f.ArticleID})
	}
	return exportDownload{
		body:     zipStream{ctx: ctx, s: s, articles: articles, markdown: markdown, files: streamed},
		filename: strings.TrimSuffix(root.path, ".md") + ".zip",
	}, nil
}

// The export's bounds (review 3): checked before anything is written.
var (
	exportMaxEntries       = 2000
	exportMaxBytes   int64 = 1 << 30
)

const codeExportTooLarge = "export_too_large"

// exportFile is one file an export writes: where, and its object.
type exportFile struct {
	path    string
	key     string
	article uuid.UUID
}

// zipStream writes the export's zip straight into the response, never
// holding it whole: each .md as it is written, each file streamed from the
// store. A file the store no longer holds is left out; a store that fails
// once the response has begun ends it, cut short, and is logged.
type zipStream struct {
	ctx      context.Context
	s        *server
	articles []*exportArticle
	markdown func(*exportArticle) string
	files    []exportFile
}

func (z zipStream) VisitExportKnowledgeArticleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/zip")
	w.WriteHeader(http.StatusOK)
	zw := zip.NewWriter(w)
	for _, a := range z.articles {
		f, err := zw.Create(a.path)
		if err != nil {
			return fmt.Errorf("knowledge: write the export: %w", err)
		}
		if _, err := io.WriteString(f, z.markdown(a)); err != nil {
			return fmt.Errorf("knowledge: write the export: %w", err)
		}
	}
	for _, file := range z.files {
		body, err := z.s.objects.get(z.ctx, file.key)
		if errors.Is(err, storage.ErrNotExist) {
			continue
		}
		if err != nil {
			z.s.deps.Logger.ErrorContext(z.ctx, "knowledge: a file could not be read for an export", "article_id", file.article.String(), "error", err.Error())
			return fmt.Errorf("knowledge: read a file for the export: %w", err)
		}
		f, err := zw.Create(file.path)
		if err == nil {
			_, err = io.Copy(f, body)
		}
		_ = body.Close()
		if err != nil {
			return fmt.Errorf("knowledge: write a file into the export: %w", err)
		}
	}
	return zw.Close()
}

// safeName is a title as a file or folder name: no separator, reserved or
// control character, at most 100 characters, "Untitled" for nothing.
func safeName(title string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return '-'
		}
		return r
	}, title)
	name = strings.Trim(strings.TrimSpace(name), ".")
	if runes := []rune(name); len(runes) > 100 {
		name = strings.TrimSpace(string(runes[:100]))
	}
	if name == "" {
		return "Untitled"
	}
	return name
}

// uniqueName is name in dir, " (2)", " (3)"… after it when a sibling has it.
func uniqueName(taken map[string]bool, dir, name string) string {
	candidate := name
	for i := 2; taken[strings.ToLower(dir+candidate)]; i++ {
		candidate = fmt.Sprintf("%s (%d)", name, i)
	}
	taken[strings.ToLower(dir+candidate)] = true
	return candidate
}

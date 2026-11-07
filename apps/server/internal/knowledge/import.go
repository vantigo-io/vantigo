package knowledge

// Markdown import (knowledge design K23): one .md, or a .zip — an Obsidian
// vault or a Notion export — whose folders become the tree. A folder and the
// .md of the same name beside it are one article, as both tools lay them out;
// a folder with no .md of its own is an empty article. [[wiki links]] and
// relative links between the imported files become article links, and the
// JPEG and PNG images the files show become their articles' files.
//
// The archive is judged before anything is written: at most 50 MiB, 500
// entries and 200 MiB uncompressed — counted as it is read, never trusted
// from its headers — no entry outside it, folders at most 10 deep. The
// images are written to the object store before the one transaction that
// creates every article, and removed again when it does not commit.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/knowledge/doc"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
)

const (
	// maxImportBytes is the largest upload an import takes.
	maxImportBytes = 50 << 20
	// maxImportEntries is how many entries a zip may hold.
	maxImportEntries = 500
	// maxImportUncompressed is how much a zip may hold uncompressed.
	maxImportUncompressed = 200 << 20
	// maxImportDepth is how deep a zip's folders may go.
	maxImportDepth = 10
	// maxImportMarkdown is the largest .md an import reads.
	maxImportMarkdown = 4 << 20

	codeImportBusy = "import_busy"
)

// notionSuffix is the id Notion appends to an exported page's name.
var notionSuffix = regexp.MustCompile(`\s+[0-9a-fA-F]{32}$`)

// errImportFile is a refusal of the uploaded file itself, with the reason.
type errImportFile struct{ reason string }

func (e errImportFile) Error() string { return e.reason }

// importNode is one article an import creates: the key is its path without
// .md, from the archive's root.
type importNode struct {
	key      string
	title    string
	id       uuid.UUID
	md       []byte
	hasMD    bool
	dir      string // the folder its .md is in
	children []*importNode
	parent   *importNode
	doc      document
}

// pendingFile is an image an import has written to the store, to record
// in the transaction; its bytes are not kept.
type pendingFile struct {
	id      uuid.UUID
	article uuid.UUID
	key     string
	upload  upload
	size    int64
}

// importSlots bounds how many imports run at once in this process (review
// 4): an import holds its upload and one image at a time in memory.
var importSlots = make(chan struct{}, 2)

// importRetryAfter is what a busy import answers in Retry-After, in seconds.
const importRetryAfter = 10

// archive is a zip's entries as an import reads them.
type archive struct {
	markdown map[string][]byte    // .md path → bytes
	others   map[string]*zip.File // lower-case path → entry
	byBase   map[string]*zip.File // lower-case base name → entry
	strip    string               // the one top folder every entry is in, left out
	read     int64                // bytes read uncompressed so far
}

// readEntry reads one entry, at most limit bytes of it, counting it against
// the archive's uncompressed bound.
func (a *archive) readEntry(f *zip.File, limit int64) ([]byte, bool, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, false, errImportFile{"The zip could not be read."}
	}
	defer func() { _ = rc.Close() }()
	room := maxImportUncompressed - a.read
	data, err := io.ReadAll(io.LimitReader(rc, min(limit, room)+1))
	if err != nil {
		return nil, false, errImportFile{"The zip could not be read."}
	}
	a.read += int64(len(data))
	if a.read > maxImportUncompressed {
		return nil, false, errImportFile{"The zip holds more than 200 MiB uncompressed."}
	}
	if int64(len(data)) > limit {
		return nil, false, nil
	}
	return data, true, nil
}

// entryPath is an entry's name as a clean relative path, and false for one
// that would reach outside the archive.
func entryPath(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if seg == ".." {
			return "", false
		}
	}
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// hidden reports whether a path is one an import leaves out: macOS's
// resource forks and any dot-file or dot-folder (.obsidian, .DS_Store).
func hidden(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "__MACOSX" {
			return true
		}
	}
	return false
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown"
}

// readArchive judges and reads a zip: its .md files whole, the rest by name.
func readArchive(data []byte, warn func(string)) (*archive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errImportFile{"The file is not a zip that can be read."}
	}
	if len(zr.File) > maxImportEntries {
		return nil, errImportFile{fmt.Sprintf("The zip holds more than %d files.", maxImportEntries)}
	}
	a := &archive{markdown: map[string][]byte{}, others: map[string]*zip.File{}, byBase: map[string]*zip.File{}}
	type entry struct {
		path string
		file *zip.File
	}
	var entries []entry
	for _, f := range zr.File {
		p, ok := entryPath(f.Name)
		if !ok {
			return nil, errImportFile{"The zip holds a path outside it."}
		}
		if f.FileInfo().IsDir() || hidden(p) {
			continue
		}
		if strings.Count(p, "/") > maxImportDepth {
			return nil, errImportFile{fmt.Sprintf("The zip's folders are more than %d deep.", maxImportDepth)}
		}
		entries = append(entries, entry{p, f})
	}
	// A zip whose every entry is in one top folder (a Notion export) is read
	// from inside it, unless that folder is a page of its own.
	if len(entries) > 0 {
		top, _, nested := strings.Cut(entries[0].path, "/")
		all := nested
		for _, e := range entries {
			if t, _, ok := strings.Cut(e.path, "/"); !ok || t != top {
				all = false
				break
			}
		}
		if all {
			a.strip = top + "/"
		}
	}
	for _, e := range entries {
		p := strings.TrimPrefix(e.path, a.strip)
		if isMarkdown(p) {
			data, ok, err := a.readEntry(e.file, maxImportMarkdown)
			if err != nil {
				return nil, err
			}
			if !ok {
				warn(fmt.Sprintf("%s is larger than 4 MiB and was imported empty.", p))
				data = nil
			}
			a.markdown[p] = data
			continue
		}
		a.others[strings.ToLower(p)] = e.file
		base := strings.ToLower(path.Base(p))
		if _, dup := a.byBase[base]; !dup {
			a.byBase[base] = e.file
		}
	}
	if len(a.markdown) == 0 {
		return nil, errImportFile{"The zip holds no .md file."}
	}
	return a, nil
}

// titleOf is an article's title from its file or folder name: without .md
// and Notion's id, at most 200 characters.
func titleOf(name string) string {
	name = strings.TrimSpace(notionSuffix.ReplaceAllString(name, ""))
	if runes := []rune(name); len(runes) > maxTitle {
		name = string(runes[:maxTitle])
	}
	return name
}

// buildTree is the articles of the .md files: a folder and the .md of its
// name one article, a folder of its own an empty one, siblings by title.
func buildTree(markdown map[string][]byte) []*importNode {
	byKey := map[string]*importNode{}
	var ensure func(key string) *importNode
	ensure = func(key string) *importNode {
		if n, ok := byKey[key]; ok {
			return n
		}
		n := &importNode{key: key, title: titleOf(path.Base(key)), id: uuid.New()}
		byKey[key] = n
		if dir := path.Dir(key); dir != "." {
			n.parent = ensure(dir)
		}
		return n
	}
	for p, data := range markdown {
		key := strings.TrimSuffix(p, path.Ext(p))
		n := ensure(key)
		n.md, n.hasMD, n.dir = data, true, path.Dir(p)
	}
	var roots []*importNode
	for _, n := range byKey {
		if n.parent == nil {
			roots = append(roots, n)
		} else {
			n.parent.children = append(n.parent.children, n)
		}
	}
	var sortNodes func([]*importNode)
	sortNodes = func(list []*importNode) {
		slices.SortFunc(list, func(a, b *importNode) int {
			if c := strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title)); c != 0 {
				return c
			}
			return strings.Compare(a.key, b.key)
		})
		for _, n := range list {
			sortNodes(n.children)
		}
	}
	sortNodes(roots)
	return roots
}

// preorder is the tree's articles, each before its children.
func preorder(roots []*importNode) []*importNode {
	var out []*importNode
	var walk func([]*importNode)
	walk = func(list []*importNode) {
		for _, n := range list {
			out = append(out, n)
			walk(n.children)
		}
	}
	walk(roots)
	return out
}

// height is how many levels the tree has.
func height(roots []*importNode) int {
	h := 0
	for _, n := range roots {
		h = max(h, 1+height(n.children))
	}
	return h
}

// ImportKnowledgeMarkdown imports one .md or a .zip under a parent the caller
// may write, or as roots in a section (K23).
func (s *server) ImportKnowledgeMarkdown(ctx context.Context, req gen.ImportKnowledgeMarkdownRequestObject) (gen.ImportKnowledgeMarkdownResponseObject, error) {
	fail := func(err error) (gen.ImportKnowledgeMarkdownResponseObject, error) {
		var bad errImportFile
		if errors.As(err, &bad) {
			err = invalid("file", "", bad.reason)
		}
		return answer(ctx, s, err, answers[gen.ImportKnowledgeMarkdownResponseObject]{
			badRequest: func(p gen.KnowledgeProblem) gen.ImportKnowledgeMarkdownResponseObject {
				return gen.ImportKnowledgeMarkdown400ApplicationProblemPlusJSONResponse(p)
			},
			forbidden: func(p gen.KnowledgeProblem) gen.ImportKnowledgeMarkdownResponseObject {
				return gen.ImportKnowledgeMarkdown403ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	select {
	case importSlots <- struct{}{}:
		defer func() { <-importSlots }()
	default:
		retry := importRetryAfter
		return gen.ImportKnowledgeMarkdown429ApplicationProblemPlusJSONResponse{
			Body: problem(http.StatusTooManyRequests, codeImportBusy, "Imports are busy",
				"Two imports are running already. Try again in a little while."),
			Headers: gen.ImportKnowledgeMarkdown429ResponseHeaders{RetryAfter: &retry},
		}, nil
	}
	form := readFileForm(req.Body, maxImportBytes)
	switch {
	case form.tooLarge:
		return gen.ImportKnowledgeMarkdown413ApplicationProblemPlusJSONResponse(fileTooLarge("50 MiB")), nil
	case form.unreadable:
		return fail(invalid("file", "", "The request's body could not be read as a multipart form."))
	case form.files > 1:
		return fail(invalid("file", "", "A request imports one file; this one carried more than one."))
	case form.files == 0 || len(form.data) == 0:
		return fail(invalid("file", "", "A .md or a .zip file is required."))
	}
	var parentID *uuid.UUID
	if raw := form.fields["parentId"]; raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return fail(invalid("parentId", codeParentInvalid, "No such article to import under."))
		}
		parentID = &id
	}
	section := gen.KnowledgeRootSectionWorkspace
	if raw := form.fields["section"]; raw != "" {
		section = gen.KnowledgeRootSection(raw)
		if section != gen.KnowledgeRootSectionWorkspace && section != gen.KnowledgeRootSectionPrivate {
			return fail(invalid("section", "", "Must be workspace or private."))
		}
	}

	var warnings []string
	warned := map[string]bool{}
	warn := func(msg string) {
		if !warned[msg] {
			warned[msg] = true
			warnings = append(warnings, msg)
		}
	}

	// Read what was uploaded into the articles to create.
	var (
		roots []*importNode
		arc   *archive
	)
	name := cleanFileName(form.filename, "import.md")
	switch {
	case bytes.HasPrefix(form.data, []byte("PK\x03\x04")) || strings.EqualFold(path.Ext(name), ".zip"):
		var err error
		if arc, err = readArchive(form.data, warn); err != nil {
			return fail(err)
		}
		roots = buildTree(arc.markdown)
	case isMarkdown(name):
		if len(form.data) > maxImportMarkdown {
			return fail(invalid("file", "", "A .md file can be at most 4 MiB."))
		}
		roots = []*importNode{{key: strings.TrimSuffix(name, path.Ext(name)), title: titleOf(strings.TrimSuffix(name, path.Ext(name))),
			id: uuid.New(), md: form.data, hasMD: true, dir: "."}}
	default:
		return fail(invalid("file", "", "The file must be a .md or a .zip."))
	}
	if height(roots) > maxDepth {
		return fail(invalid("file", codeTooDeep, fmt.Sprintf("The tree is at most %d levels deep.", maxDepth)))
	}
	nodes := preorder(roots)
	byKey := map[string]*importNode{}
	byTitle := map[string]*importNode{}
	for _, n := range nodes {
		byKey[strings.ToLower(n.key)] = n
		if _, dup := byTitle[strings.ToLower(n.title)]; !dup {
			byTitle[strings.ToLower(n.title)] = n
		}
	}
	lookup := func(target string) (string, bool) {
		t := strings.ToLower(strings.TrimSpace(target))
		t = strings.TrimSuffix(strings.TrimSuffix(t, ".md"), ".markdown")
		if n, ok := byKey[t]; ok {
			return n.id.String(), true
		}
		if n, ok := byTitle[strings.ToLower(titleOf(path.Base(t)))]; ok {
			return n.id.String(), true
		}
		return "", false
	}

	// Convert every .md, uploading the images it shows.
	// Every image is written to the store as it is read, and removed again
	// when the import does not complete.
	var (
		pending  []pendingFile
		keys     []string
		storeErr error
	)
	cleanup := func() {
		if len(keys) > 0 {
			removeObjects(ctx, s.deps, s.objects, nodes[0].id, keys...)
		}
	}
	user := callerID(ctx)
	for _, n := range nodes {
		if n.hasMD && len(n.md) > 0 {
			if !utf8.Valid(n.md) {
				warn(fmt.Sprintf("%s is not UTF-8 text and was imported empty.", n.key))
			} else {
				fileIDs := map[string]string{}
				var readErr error
				im := doc.Importer{
					Wiki: lookup,
					Relative: func(dest string) (string, bool) {
						dest, _, _ = strings.Cut(dest, "#")
						if dest == "" {
							return "", false
						}
						return lookup(path.Join(n.dir, dest))
					},
					Image: func(dest string) (string, bool) {
						if arc == nil || readErr != nil || storeErr != nil {
							return "", false
						}
						f, ok := arc.others[strings.ToLower(path.Clean(path.Join(n.dir, dest)))]
						if !ok {
							f, ok = arc.byBase[strings.ToLower(path.Base(dest))]
						}
						if !ok {
							return "", false
						}
						if id, done := fileIDs[f.Name]; done {
							return id, true
						}
						data, within, err := arc.readEntry(f, maxFileBytes)
						if err != nil {
							readErr = err
							return "", false
						}
						if !within {
							warn(fmt.Sprintf("%s is larger than 25 MiB and was left out.", path.Base(f.Name)))
							return "", false
						}
						u := classify(data, path.Base(f.Name), "")
						if !u.isImage {
							warn(fmt.Sprintf("%s is not a JPEG or a PNG and was left out.", path.Base(f.Name)))
							return "", false
						}
						id, key := uuid.New(), fileObjectKey(n.id)
						if err := s.objects.put(ctx, key, u.data, u.contentType); err != nil {
							s.deps.Logger.ErrorContext(ctx, "knowledge: an imported image could not be written to the store", "article_id", n.id.String(), "error", err.Error())
							storeErr = err
							return "", false
						}
						keys = append(keys, key)
						size := int64(len(u.data))
						u.data = nil
						pending = append(pending, pendingFile{id: id, article: n.id, key: key, upload: u, size: size})
						fileIDs[f.Name] = id.String()
						return id.String(), true
					},
					Warn: warn,
				}
				body := doc.FromMarkdown(n.md, im)
				n.md = nil
				if storeErr != nil {
					cleanup()
					return gen.ImportKnowledgeMarkdown503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
				}
				if readErr != nil {
					cleanup()
					return fail(readErr)
				}
				dropTitleHeading(body, n.title)
				// What the allowlist refuses is left out, one thing at a
				// time, never the whole article (review 5).
				repaired, parsed, err := doc.Repair(body, warn)
				if err == nil {
					n.doc = document{raw: repaired, text: doc.Text(parsed), refs: doc.References(parsed)}
					continue
				}
				warn(fmt.Sprintf("%s is larger than an article can be and was imported empty.", n.key))
			}
		}
		n.md = nil
		parsed, err := doc.Parse(emptyDocument)
		if err != nil {
			return nil, fmt.Errorf("knowledge: the empty document: %w", err)
		}
		n.doc = document{raw: emptyDocument, text: doc.Text(parsed), refs: doc.References(parsed)}
	}

	now := s.deps.Clock()
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		var own *string
		if parentID != nil {
			parent, err := loadMeta(ctx, q, *parentID, user)
			if err != nil && !isNotFound(err) {
				return err
			}
			if err != nil || !readable(parent) {
				return invalid("parentId", codeParentInvalid, "No such article to import under.")
			}
			if parent.Permission != levelWrite {
				return readOnly()
			}
			depth, err := q.ArticleDepth(ctx, parent.ID)
			if err != nil {
				return fmt.Errorf("knowledge: read the depth of %s: %w", parent.ID, err)
			}
			if int(depth)+height(roots) > maxDepth {
				return invalid("parentId", codeTooDeep, fmt.Sprintf("The tree is at most %d levels deep.", maxDepth))
			}
		} else {
			l := levelWrite
			if section == gen.KnowledgeRootSectionPrivate {
				l = levelNone
			}
			own = &l
		}
		prev, err := q.LastSiblingPosition(ctx, store.LastSiblingPositionParams{ParentID: parentID, Exclude: uuid.Nil})
		if err != nil {
			return fmt.Errorf("knowledge: read the last position: %w", err)
		}
		last := map[uuid.UUID]string{}
		for _, n := range nodes {
			var parent *uuid.UUID
			after := prev
			perm := own
			if n.parent != nil {
				parent, after, perm = &n.parent.id, last[n.parent.id], nil
			} else {
				parent = parentID
			}
			position, err := keyBetween(after, "")
			if err != nil {
				return fmt.Errorf("knowledge: a key after %q: %w", after, err)
			}
			if n.parent != nil {
				last[n.parent.id] = position
			} else {
				prev = position
			}
			if err := q.InsertArticle(ctx, store.InsertArticleParams{
				ID: n.id, ParentID: parent, Position: position, Title: n.title, Content: n.doc.raw, ContentText: n.doc.text,
				InternalPermission: perm, UserID: user, Now: now,
			}); err != nil {
				return fmt.Errorf("knowledge: create an imported article: %w", err)
			}
			if err := q.InsertVersion(ctx, store.InsertVersionParams{
				ArticleID: n.id, Version: 1, Title: n.title, Content: n.doc.raw, CreatedBy: &user, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("knowledge: record an imported article's first version: %w", err)
			}
			if n.parent == nil && parentID == nil && section == gen.KnowledgeRootSectionPrivate {
				if err := q.UpsertMember(ctx, store.UpsertMemberParams{ArticleID: n.id, UserID: user, Permission: levelWrite}); err != nil {
					return fmt.Errorf("knowledge: make the importer a writer: %w", err)
				}
			}
		}
		for _, r := range roots {
			if err := recompute(ctx, q, r.id); err != nil {
				return err
			}
		}
		for _, p := range pending {
			if err := q.InsertFile(ctx, store.InsertFileParams{
				ID: p.id, ArticleID: p.article, ObjectKey: p.key, Filename: p.upload.filename, ContentType: p.upload.contentType,
				Size: p.size, IsImage: p.upload.isImage, CreatedBy: &user, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("knowledge: record an imported file: %w", err)
			}
		}
		for _, n := range nodes {
			if err := rewriteLinks(ctx, q, n.id, n.doc.refs); err != nil {
				return err
			}
		}
		return notifyTree(ctx, tx)
	})
	if err != nil {
		cleanup()
		return fail(err)
	}
	for _, n := range nodes {
		s.notifyMentions(ctx, n.id, n.title, user, newMentions(doc.Refs{}, n.doc.refs, user))
	}
	out := gen.KnowledgeImportResult{Created: make([]gen.KnowledgeImportCreated, 0, len(nodes)), Warnings: warnings}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	for _, n := range nodes {
		out.Created = append(out.Created, gen.KnowledgeImportCreated{Id: n.id, Title: n.title})
	}
	return gen.ImportKnowledgeMarkdown201JSONResponse(out), nil
}

// dropTitleHeading leaves out a document's first block when it is a heading
// that only repeats the title, as Notion's exports begin.
func dropTitleHeading(body map[string]any, title string) {
	content, _ := body["content"].([]any)
	if len(content) == 0 {
		return
	}
	first, _ := content[0].(map[string]any)
	if first["type"] != "heading" {
		return
	}
	var b strings.Builder
	for _, c := range asAnyList(first["content"]) {
		if t, ok := c.(map[string]any); ok && t["type"] == "text" {
			b.WriteString(t["text"].(string))
		}
	}
	if !strings.EqualFold(strings.TrimSpace(b.String()), strings.TrimSpace(title)) {
		return
	}
	content = content[1:]
	if len(content) == 0 {
		content = []any{map[string]any{"type": "paragraph"}}
	}
	body["content"] = content
}

func asAnyList(v any) []any {
	l, _ := v.([]any)
	return l
}

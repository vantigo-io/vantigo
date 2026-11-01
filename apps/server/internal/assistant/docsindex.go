package assistant

import (
	"embed"
	"io/fs"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// The documentation, embedded. `go generate` copies the user guide and the
// administration guide, in both languages, from docs/src/content/docs into
// docs/ here (apps/server/generate.go), the way the contract is copied into
// internal/openapi/specs; docs_fresh_test.go fails when the copy is stale.
//
//go:embed docs
var docsFS embed.FS

// docSection is one searchable section of a documentation page: the text under
// a ## or ### heading (or a page's introduction), with the page title and the
// heading path above it.
type docSection struct {
	lang, section, page string
	pageTitle           string
	headingPath         string // "Page title › Heading › Subheading"
	heading             string // the deepest heading, or the page title for an introduction
	anchor              string // Starlight's slug of heading; empty for an introduction
	text                string // cleaned Markdown
	terms               map[string]float64
	headTerms           map[string]bool // the tokens of the heading and the page title
	length              float64
}

// DocResult is one answer of search_docs.
type DocResult struct {
	Title   string `json:"title"`
	Heading string `json:"heading"`
	Path    string `json:"path"`
	Excerpt string `json:"excerpt"`
}

// docIndex is a BM25 index over one language's sections.
type docIndex struct {
	sections []*docSection
	df       map[string]int
	avgLen   float64
}

// docsIndexes are the per-language indexes, built on first use from the
// embedded copy (the work is a few milliseconds a language, and a process that
// never serves the assistant never pays it).
type docsIndexes struct {
	once sync.Once
	by   map[string]*docIndex
	err  error
}

var theDocs docsIndexes

func (d *docsIndexes) get(lang string) (*docIndex, error) {
	d.once.Do(func() {
		d.by = map[string]*docIndex{}
		for _, l := range []string{"en", "nb"} {
			idx, err := buildDocIndex(docsFS, "docs", l)
			if err != nil {
				d.err = err
				return
			}
			d.by[l] = idx
		}
	})
	if d.err != nil {
		return nil, d.err
	}
	if idx, ok := d.by[lang]; ok {
		return idx, nil
	}
	return d.by["en"], nil
}

// searchDocs answers the best sections for query in the person's language,
// falling back to English for what that language does not answer. The paths are
// the instance's own /docs/<lang>/<section>/<page>/#<anchor>.
func searchDocs(query, lang string, limit int) ([]DocResult, error) {
	if limit <= 0 {
		limit = 4
	}
	var out []DocResult
	seen := map[string]bool{}
	langs := []string{lang}
	if lang != "en" {
		langs = append(langs, "en")
	}
	for _, l := range langs {
		idx, err := theDocs.get(l)
		if err != nil {
			return nil, err
		}
		// The fallback fills what the person's language did not answer well.
		if l == "en" && lang != "en" && len(out) >= limit-1 {
			break
		}
		for _, sec := range idx.search(query, limit) {
			if seen[sec.pageTitle+"|"+sec.heading] || len(out) >= limit {
				continue
			}
			seen[sec.pageTitle+"|"+sec.heading] = true
			out = append(out, sec.result())
		}
	}
	return out, nil
}

func (s *docSection) result() DocResult {
	p := "/docs/" + s.lang + "/" + s.section + "/"
	if s.page != "index" {
		p += s.page + "/"
	}
	if s.anchor != "" {
		p += "#" + s.anchor
	}
	return DocResult{Title: s.pageTitle, Heading: s.heading, Path: p, Excerpt: excerpt(s.text, 700)}
}

// excerpt is text cut at a word boundary within max characters.
func excerpt(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= max {
		return text
	}
	cut := []rune(text)[:max]
	i := len(cut)
	for i > max/2 && !unicode.IsSpace(cut[i-1]) {
		i--
	}
	return strings.TrimSpace(string(cut[:i])) + "…"
}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// synonyms widen a question's verbs to the ones the documentation uses for the
// same task: a person asks to "add" what a heading calls "Create".
var synonyms = map[string][]string{
	"add": {"create", "new"}, "make": {"create"}, "register": {"create"}, "set": {"configure"},
	"remove": {"delete"}, "erase": {"delete"}, "change": {"edit"}, "update": {"edit"},
	"legge": {"opprett", "ny"}, "legger": {"opprett", "ny"}, "lage": {"opprett", "ny"}, "lager": {"opprett", "ny"},
	"registrere": {"opprett"}, "fjerne": {"slett"}, "fjerner": {"slett"}, "endre": {"rediger"}, "endrer": {"rediger"},
}

func (idx *docIndex) search(query string, limit int) []*docSection {
	var tokens []string
	for _, tok := range tokenizeDocs(query) {
		tokens = append(tokens, tok)
		for _, syn := range synonyms[tok] {
			tokens = append(tokens, stem(syn))
		}
	}
	if len(tokens) == 0 || len(idx.sections) == 0 {
		return nil
	}
	n := float64(len(idx.sections))
	type scored struct {
		sec   *docSection
		score float64
	}
	var hits []scored
	for _, sec := range idx.sections {
		score := 0.0
		for _, tok := range tokens {
			tf := sec.terms[tok]
			if tf == 0 {
				continue
			}
			df := float64(idx.df[tok])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*sec.length/idx.avgLen))
			// A question usually names what the section is about: a word in the
			// heading counts for more than a long table repeating it.
			if sec.headTerms[tok] {
				score += idf * 1.5
			}
		}
		if score > 0 {
			hits = append(hits, scored{sec, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*docSection, len(hits))
	for i, h := range hits {
		out[i] = h.sec
	}
	return out
}

// docStopWords say nothing about which section a question is about, in the two
// languages the documentation is written in.
var docStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true, "on": true, "for": true, "and": true, "or": true,
	"how": true, "do": true, "does": true, "can": true, "i": true, "me": true, "my": true, "is": true, "are": true, "with": true,
	"it": true, "this": true, "that": true, "be": true, "you": true, "your": true, "what": true, "where": true,
	"en": true, "et": true, "ei": true, "og": true, "på": true, "av": true, "til": true, "som": true, "jeg": true,
	"hvordan": true, "kan": true, "er": true, "det": true, "den": true, "med": true, "du": true, "har": true,
	"hva": true, "hvor": true, "ikke": true, "å": true, "de": true,
}

// stem trims the common inflections of English and Norwegian off a word, so
// "invoices" finds "invoice" and "fakturaene" finds "faktura". It is crude on
// purpose: the same function runs over the documentation and the query.
func stem(w string) string {
	for _, suffix := range []string{"ene", "ers", "ing", "er", "en", "et", "es", "ed", "s"} {
		if strings.HasSuffix(w, suffix) && len([]rune(w))-len(suffix) >= 4 {
			return strings.TrimSuffix(w, suffix)
		}
	}
	return w
}

// tokenizeDocs lower-cases s, splits it into words, drops stop words and stems.
func tokenizeDocs(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := words[:0]
	for _, w := range words {
		if docStopWords[w] || len([]rune(w)) < 2 {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

func buildDocIndex(fsys fs.FS, root, lang string) (*docIndex, error) {
	idx := &docIndex{df: map[string]int{}}
	for _, section := range []string{"user", "admin"} {
		dir := path.Join(root, lang, section)
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			page := strings.TrimSuffix(e.Name(), ".md")
			idx.sections = append(idx.sections, splitPage(lang, section, page, string(raw))...)
		}
	}
	total := 0.0
	for _, sec := range idx.sections {
		sec.terms = map[string]float64{}
		add := func(text string, weight float64) {
			for _, tok := range tokenizeDocs(text) {
				sec.terms[tok] += weight
				sec.length += weight
			}
		}
		sec.headTerms = map[string]bool{}
		for _, tok := range tokenizeDocs(sec.heading + " " + sec.pageTitle) {
			sec.headTerms[tok] = true
		}
		add(sec.pageTitle, 2)
		add(sec.heading, 3)
		add(sec.text, 1)
		for tok := range sec.terms {
			idx.df[tok]++
		}
		total += sec.length
	}
	if len(idx.sections) > 0 {
		idx.avgLen = total / float64(len(idx.sections))
	}
	return idx, nil
}

var (
	frontTitle  = regexp.MustCompile(`(?m)^title:\s*(.+?)\s*$`)
	mdLink      = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTag     = regexp.MustCompile(`<[^>]+>`)
	asideMarker = regexp.MustCompile(`^:::\w*(\[[^\]]*\])?\s*$`)
)

// splitPage cuts a page at its ## and ### headings. The text before the first
// is the page's introduction.
func splitPage(lang, section, page, raw string) []*docSection {
	title := page
	body := raw
	if strings.HasPrefix(raw, "---") {
		if end := strings.Index(raw[3:], "\n---"); end >= 0 {
			header := raw[3 : 3+end]
			if m := frontTitle.FindStringSubmatch(header); m != nil {
				title = strings.Trim(m[1], `"' `)
			}
			body = raw[3+end+4:]
		}
	}

	var sections []*docSection
	slugs := newSlugger()
	cur := &docSection{lang: lang, section: section, page: page, pageTitle: title, heading: title, headingPath: title}
	var h2 string
	var text strings.Builder
	flush := func() {
		cur.text = cleanMarkdown(text.String())
		if cur.text != "" || cur.anchor != "" {
			sections = append(sections, cur)
		}
		text.Reset()
	}
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
		}
		if !fenced {
			level, heading := headingOf(line)
			if level >= 1 {
				anchor := slugs.slug(heading) // every heading takes a slug, so duplicates count
				if level == 2 || level == 3 {
					flush()
					path := title + " › " + heading
					if level == 3 && h2 != "" {
						path = title + " › " + h2 + " › " + heading
					} else if level == 2 {
						h2 = heading
					}
					cur = &docSection{lang: lang, section: section, page: page, pageTitle: title, heading: heading, headingPath: path, anchor: anchor}
				}
				continue
			}
		}
		text.WriteString(line)
		text.WriteByte('\n')
	}
	flush()
	return sections
}

// headingOf is the level and text of an ATX heading line, or 0.
func headingOf(line string) (int, string) {
	if !strings.HasPrefix(line, "#") {
		return 0, ""
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[level:]), "#"))
}

// cleanMarkdown reduces a section to the words a model reads: links to their
// text, no HTML or Starlight aside markers, code fences' markers dropped.
func cleanMarkdown(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if asideMarker.MatchString(t) || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || strings.HasPrefix(t, "import ") {
			continue
		}
		lines = append(lines, line)
	}
	s = strings.Join(lines, "\n")
	s = mdLink.ReplaceAllString(s, "$1")
	s = htmlTag.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return strings.TrimSpace(s)
}

// slugger slugs headings the way Starlight (github-slugger) does: lower-case,
// punctuation removed, spaces to hyphens, a repeated slug suffixed -1, -2.
type slugger struct{ seen map[string]int }

func newSlugger() *slugger { return &slugger{seen: map[string]int{}} }

func (g *slugger) slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.M, r) || r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	original := b.String()
	slug := original
	for {
		if _, taken := g.seen[slug]; !taken {
			break
		}
		g.seen[original]++
		slug = original + "-" + strconv.Itoa(g.seen[original])
	}
	g.seen[slug] = 0
	return slug
}

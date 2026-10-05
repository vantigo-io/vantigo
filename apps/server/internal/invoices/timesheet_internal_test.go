package invoices

import (
	"bytes"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/core"
)

// pageObject is one page object of a PDF gofpdf wrote: "/Type /Page", not
// "/Type /Pages".
var pageObject = regexp.MustCompile(`/Type /Page[^s]`)

// A timesheet of a few hundred rows renders without error over several
// pages after the invoice's own: maroto's AddRows breaks it where a page
// ends, and every row is in the model.
func TestPDF_TheTimesheetPaginates(t *testing.T) {
	t.Parallel()
	plain, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render the invoice alone: %v", err)
	}
	d := anInvoice()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 300 {
		d.timesheet = append(d.timesheet, pdfTimesheetRow{
			date: day.AddDate(0, 0, i%30), person: fmt.Sprintf("P%d", i%7), workType: "Rådgivning",
			description: "Prosjekt 41", hours: big.NewRat(int64(25+i%400), 100),
		})
	}
	m := buildPDFModel(d)
	if m.timesheet == nil || len(m.timesheet.rows) != 300 || len(m.timesheet.totals) != 8 {
		t.Fatalf("the model's timesheet = %+v; want 300 rows and totals for seven people and the whole", m.timesheet)
	}
	body, err := renderPDF(m)
	if err != nil {
		t.Fatalf("render with the timesheet: %v", err)
	}
	alone, with := len(pageObject.FindAll(plain, -1)), len(pageObject.FindAll(body, -1))
	if alone != 1 || with < alone+4 || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Errorf("pages = %d alone and %d with 300 rows, want 1 and at least five", alone, with)
	}

	// On pages of its own, the header on every one: the document's page
	// carries its totals and no timesheet, every later page the column
	// header, the first of them the title, and every row is on one of them.
	doc, err := layoutPDF(m)
	if err != nil {
		t.Fatalf("lay out: %v", err)
	}
	pages := pageTexts(doc.GetStructure())
	if len(pages) != with {
		t.Fatalf("the layout has %d pages, the PDF %d", len(pages), with)
	}
	if first := pages[0]; !slices.Contains(first, "Å betale") || slices.Contains(first, "Timeliste") || slices.Contains(first, "Dato") {
		t.Errorf("the document's page = %v, want its totals and no timesheet", first)
	}
	if !slices.Contains(pages[1], "Timeliste") {
		t.Errorf("the timesheet's first page has no title: %v", pages[1][:min(8, len(pages[1]))])
	}
	rows := 0
	for i, texts := range pages[1:] {
		if !slices.Contains(texts, "Dato") || !slices.Contains(texts, "Beskrivelse") || !slices.Contains(texts, "Timer") {
			t.Errorf("timesheet page %d has no column header", i+1)
		}
		rows += countOf(texts, "Prosjekt 41")
	}
	if rows != 300 {
		t.Errorf("%d rows on the timesheet's pages, want 300", rows)
	}
}

// pageTexts is every text a laid-out document prints, page by page, from
// maroto's own structure of it.
func pageTexts(root interface {
	GetNexts() []*node.Node[core.Structure]
}) [][]string {
	var out [][]string
	var walk func(n *node.Node[core.Structure], page *[]string)
	walk = func(n *node.Node[core.Structure], page *[]string) {
		if d := n.GetData(); d.Type == "text" {
			*page = append(*page, fmt.Sprint(d.Value))
		}
		for _, next := range n.GetNexts() {
			walk(next, page)
		}
	}
	for _, p := range root.GetNexts() {
		var texts []string
		walk(p, &texts)
		out = append(out, texts)
	}
	return out
}

func countOf(texts []string, s string) int {
	n := 0
	for _, t := range texts {
		if t == s {
			n++
		}
	}
	return n
}

// No two people share a label, whatever their names: a suffixed label that
// is someone's own takes the next free number, in initials and name mode.
func TestPersonLabels_NeverCollide(t *testing.T) {
	t.Parallel()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for _, c := range []struct {
		mode  string
		names []string
		want  []string
	}{
		{labelInitials, []string{"Kari Nordmann", "Knut Nilsen", "K N 2", "Kåre Næss"}, []string{"KN", "KN2", "KN22", "KN3"}},
		{labelInitials, []string{"K N 2", "Kari Nordmann", "Knut Nilsen", "Ola Hansen"}, []string{"KN2", "KN", "KN3", "OH"}},
		{labelName, []string{"Ola", "Ola", "Ola 2", "Ola"}, []string{"Ola", "Ola 2", "Ola 2 2", "Ola 3"}},
		{labelNumber, []string{"Ola", "Ola", "Kari", ""}, []string{"Person 1", "Person 2", "Person 3", "Person 4"}},
	} {
		names := map[uuid.UUID]string{}
		for i, n := range c.names {
			if n != "" {
				names[ids[i]] = n
			}
		}
		got := personLabels(c.mode, names, ids)
		for i, id := range ids {
			if got[id] != c.want[i] {
				t.Errorf("%s %v: person %d = %q, want %q", c.mode, c.names, i, got[id], c.want[i])
			}
		}
	}
}

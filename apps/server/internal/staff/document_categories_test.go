package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func documentCategoryPath(id int64) string { return rowPath(documentCategoriesPath, id) }

// listCategories reads the categories and fails the test unless it answered
// 200.
func listCategories(t *testing.T, h *harness) []gen.StaffDocumentCategory {
	t.Helper()
	hr, _ := signIn(t, h, "staff:manage")
	var out []gen.StaffDocumentCategory
	do(t, hr, http.MethodGet, documentCategoriesPath, nil, http.StatusOK, &out)
	return out
}

// The six seeds in their order with their classes, restricted exactly for
// health; the list is staff:view's, staff:manage's and staff:personal-data's,
// and the uniform 403 to anyone else.
func TestDocumentCategories_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var keys, classes []string
	for _, c := range listCategories(t, h) {
		keys = append(keys, c.Key)
		classes = append(classes, string(c.Class))
		if c.Restricted != (c.Class == gen.StaffDocumentClassE) || !c.Seeded || c.ArchivedAt != nil {
			t.Errorf("seed %s = %+v, want restricted exactly for class E, seeded, not archived", c.Key, c)
		}
	}
	if !slices.Equal(keys, []string{"contract", "attest", "certificate", "correspondence", "health", "other"}) ||
		!slices.Equal(classes, []string{"A", "A", "D", "D", "E", "D"}) {
		t.Errorf("categories = %v %v, want the six seeds with classes A A D D E D", keys, classes)
	}
	for _, perms := range [][]string{{"staff:view"}, {"staff:personal-data"}} {
		c, _ := signIn(t, h, perms...)
		do(t, c, http.MethodGet, documentCategoriesPath, nil, http.StatusOK, nil)
	}
	nobody, _ := signIn(t, h)
	refused(t, nobody.Do(http.MethodGet, documentCategoriesPath, nil), http.StatusForbidden, "")
}

// A company category: a key of a–z, 0–9 and hyphens, unique; names in both
// languages; a class of A, D or E, fixed — restricted following from it.
// Added after the seeds, by staff:manage alone.
func TestDocumentCategories_Add(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	names := map[string]any{"en": "Courses", "nb": "Kurs"}

	var added gen.StaffDocumentCategory
	do(t, hr, http.MethodPost, documentCategoriesPath, map[string]any{"key": "courses", "names": names, "class": "D"}, http.StatusCreated, &added)
	if added.Key != "courses" || added.Class != gen.StaffDocumentClassD || added.Restricted || added.Seeded || added.Sort != 7 ||
		added.Names.En != "Courses" || added.Names.Nb != "Kurs" || added.Revision != 1 {
		t.Errorf("added = %+v, want courses, class D, not restricted, after the seeds", added)
	}
	var occupational gen.StaffDocumentCategory
	do(t, hr, http.MethodPost, documentCategoriesPath,
		map[string]any{"key": "occupational-health", "names": map[string]any{"en": "Occupational health", "nb": "Bedriftshelse"}, "class": "E"},
		http.StatusCreated, &occupational)
	if !occupational.Restricted {
		t.Errorf("a class E category = %+v, want restricted", occupational)
	}

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a key another category has", map[string]any{"key": "contract", "names": names, "class": "A"}, "key"},
		{"a key with capitals", map[string]any{"key": "Courses2", "names": names, "class": "D"}, "key"},
		{"a key of 41 characters", map[string]any{"key": "a23456789012345678901234567890123456789012", "names": names, "class": "D"}, "key"},
		{"an empty key", map[string]any{"key": "", "names": names, "class": "D"}, "key"},
		{"no Norwegian name", map[string]any{"key": "x", "names": map[string]any{"en": "X", "nb": " "}, "class": "D"}, "names.nb"},
		{"class C", map[string]any{"key": "x", "names": names, "class": "C"}, "class"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refused(t, hr.Do(http.MethodPost, documentCategoriesPath, tc.body), http.StatusBadRequest, "")
			if len(p.Errors[tc.field]) == 0 && tc.field != "class" {
				t.Errorf("errors = %v, want %s named", p.Errors, tc.field)
			}
		})
	}
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")
	do(t, viewer, http.MethodPost, documentCategoriesPath, map[string]any{"key": "y", "names": names, "class": "D"}, http.StatusForbidden, nil)
}

// A company category's names change and it is archived or restored under
// If-Match; a seeded one is seed_immutable, and archiving one documents are
// filed under is category_in_use. An archived category is offered for no new
// document.
func TestDocumentCategories_Change(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	var courses gen.StaffDocumentCategory
	do(t, hr, http.MethodPost, documentCategoriesPath,
		map[string]any{"key": "courses", "names": map[string]any{"en": "Courses", "nb": "Kurs"}, "class": "D"}, http.StatusCreated, &courses)
	renamed := map[string]any{"names": map[string]any{"en": "Training", "nb": "Opplæring"}, "archived": false}

	refused(t, hr.Do(http.MethodPut, documentCategoryPath(courses.Id), renamed, ifMatch(2)), http.StatusConflict, "revision_conflict")
	var got gen.StaffDocumentCategory
	do(t, hr, http.MethodPut, documentCategoryPath(courses.Id), renamed, http.StatusOK, &got, ifMatch(1))
	if got.Names.En != "Training" || got.Names.Nb != "Opplæring" || got.Class != gen.StaffDocumentClassD || got.Key != "courses" || got.Revision != 2 {
		t.Errorf("renamed = %+v, want Training/Opplæring, key and class unchanged, revision 2", got)
	}

	contract := documentCategoryID(t, h, "contract")
	refused(t, hr.Do(http.MethodPut, documentCategoryPath(contract), renamed, ifMatch(1)), http.StatusConflict, "seed_immutable")

	person := seedPerson(t, h, "Kari", "Nordmann")
	doc := uploadDocument(t, hr, person, pdfUpload(courses.Id))
	archive := map[string]any{"names": map[string]any{"en": "Training", "nb": "Opplæring"}, "archived": true}
	refused(t, hr.Do(http.MethodPut, documentCategoryPath(courses.Id), archive, ifMatch(2)), http.StatusConflict, "category_in_use")

	do(t, hr, http.MethodDelete, documentPath(doc.Id), nil, http.StatusNoContent, nil)
	do(t, hr, http.MethodPut, documentCategoryPath(courses.Id), archive, http.StatusOK, &got, ifMatch(2))
	if got.ArchivedAt == nil {
		t.Errorf("archived = %+v, want archivedAt set", got)
	}
	p := refused(t, postDocument(t, hr, person, pdfUpload(courses.Id)), http.StatusBadRequest, "")
	if len(p.Errors["categoryId"]) == 0 {
		t.Errorf("an upload into an archived category: errors = %v, want categoryId named", p.Errors)
	}
	got = gen.StaffDocumentCategory{}
	do(t, hr, http.MethodPut, documentCategoryPath(courses.Id), renamed, http.StatusOK, &got, ifMatch(3))
	if got.ArchivedAt != nil {
		t.Errorf("restored = %+v, want archivedAt absent", got)
	}
	do(t, hr, http.MethodPut, documentCategoryPath(9_999_999), renamed, http.StatusNotFound, nil, ifMatch(1))
}

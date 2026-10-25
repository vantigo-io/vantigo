package staff

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// keepRawBody keeps a copy of every body but a multipart or an octet-stream
// one, which it passes to the handler untouched and unread, so an upload is
// never held in memory by it.
func TestKeepRawBody_KeepsAllButUploads(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, contentType, body string
		kept                    bool
	}{
		{"json", "application/json", `{"givenName":"Kari"}`, true},
		{"json with a charset", "application/json; charset=utf-8", `{"givenName":"Kari"}`, true},
		{"multipart", "multipart/form-data; boundary=x", "--x\r\n\r\nfile\r\n--x--", false},
		{"octet-stream", "application/octet-stream", "\x00\x01", false},
		// The generated decoder parses JSON whatever the Content-Type, so
		// every other body is captured — fetch sends a string as text/plain.
		{"plain text", "text/plain;charset=UTF-8", `{"givenName":"Kari"}`, true},
		{"no content type", "", `{"givenName":"Kari"}`, true},
	} {
		var sawKept bool
		var sawBody string
		var sawSame bool
		original := io.NopCloser(strings.NewReader(c.body))
		handler := keepRawBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, sawKept = r.Context().Value(rawBodyKey{}).([]byte)
			sawSame = r.Body == original
			b, _ := io.ReadAll(r.Body)
			sawBody = string(b)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = original
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if sawKept != c.kept || sawBody != c.body {
			t.Errorf("%s: kept %v body %q, want kept %v and the body intact", c.name, sawKept, sawBody, c.kept)
		}
		if !c.kept && !sawSame {
			t.Errorf("%s: the handler got another reader, want the request's own body untouched", c.name)
		}
	}
}

// The mount fails unless the contract closes every schema of closedSchemas
// (additionalProperties false), and the rows of every list of closedRows:
// the unknown-property refusal reads that, and a contract that lost it — or
// the schema — would silently accept anything.
func TestCheckClosedSchemas_FailsTheMountOnAnOpenOrMissingSchema(t *testing.T) {
	t.Parallel()
	doc, err := openapi.Load(context.Background(), "staff")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkClosedSchemas(doc); err != nil {
		t.Errorf("the real contract: %v, want nil", err)
	}
	if !slices.Equal(closedSchemas, []string{personRequestSchema, accountRequestSchema, meRequestSchema, absenceTypeChangeSchema, absenceChangeSchema, absenceRequestSchema, decisionSchema, competenceChangeSchema,
		interestsRequestSchema, preferencesRequestSchema, availabilityRequestSchema, competenceReviewRequestSchema,
		competenceProfileRequestSchema, competenceProfileChangeRequestSchema, personProfilesRequestSchema}) {
		t.Errorf("closed schemas = %v, want StaffPersonRequest, StaffPersonAccountRequest, StaffMeRequest, StaffAbsenceTypeChangeRequest, StaffAbsenceChangeRequest, StaffAbsenceRequest, StaffAbsenceDecisionRequest, StaffCompetenceChangeRequest, the three wish lists, StaffCompetenceReviewRequest and the three profile requests", closedSchemas)
	}
	if want := []struct{ schema, list string }{
		{interestsRequestSchema, "interests"}, {preferencesRequestSchema, "preferences"}, {availabilityRequestSchema, "declarations"},
		{competenceProfileRequestSchema, "items"}, {competenceProfileChangeRequestSchema, "items"},
	}; !slices.Equal(closedRows, want) {
		t.Errorf("closed rows = %v, want the three wish lists' rows and the two profile requests' items", closedRows)
	}
	open := true
	for _, name := range closedSchemas {
		opened, _ := openapi.Load(context.Background(), "staff")
		opened.Components.Schemas[name].Value.AdditionalProperties = openapi3.AdditionalProperties{Has: &open}
		if err := checkClosedSchemas(opened); err == nil {
			t.Errorf("an open %s passed, want an error", name)
		}
		missing, _ := openapi.Load(context.Background(), "staff")
		delete(missing.Components.Schemas, name)
		if err := checkClosedSchemas(missing); err == nil {
			t.Errorf("a contract without %s passed, want an error", name)
		}
	}
	for _, c := range closedRows {
		opened, _ := openapi.Load(context.Background(), "staff")
		opened.Components.Schemas[c.schema].Value.Properties[c.list].Value.Items.Value.AdditionalProperties = openapi3.AdditionalProperties{}
		if err := checkClosedSchemas(opened); err == nil {
			t.Errorf("the rows of %s.%s without additionalProperties passed, want an error", c.schema, c.list)
		}
		reopened, _ := openapi.Load(context.Background(), "staff")
		reopened.Components.Schemas[c.schema].Value.Properties[c.list].Value.Items.Value.AdditionalProperties = openapi3.AdditionalProperties{Has: &open}
		if err := checkClosedSchemas(reopened); err == nil {
			t.Errorf("open rows of %s.%s passed, want an error", c.schema, c.list)
		}
	}
	if err := checkClosedSchemas(nil); err == nil {
		t.Error("no contract passed, want an error")
	}
}

// A row's property the contract does not name is reported whatever else the
// body carries: a profile's key and names beside its items — properties that
// are not lists — do not stop the items from being judged, and a list that
// is absent, null or not an array judges no rows.
func TestUnknownRowProperties_JudgesTheListBesideOtherProperties(t *testing.T) {
	t.Parallel()
	doc, err := openapi.Load(context.Background(), "staff")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{deps: module.Deps{Doc: doc}}
	for _, tc := range []struct {
		name, schema, list, body string
		want                     []string
	}{
		{"a profile's item", competenceProfileRequestSchema, "items",
			`{"key":"x","names":{"en":"X","nb":"X"},"items":[{"competenceId":1},{"competenceId":2,"level":2}]}`, []string{"items[1].level"}},
		{"a profile's change", competenceProfileChangeRequestSchema, "items",
			`{"names":{"en":"X","nb":"X"},"archived":false,"items":[{"competenceId":1,"note":"n","level":2}]}`, []string{"items[0].level", "items[0].note"}},
		{"an interest", interestsRequestSchema, "interests", `{"interests":[{"competenceId":1,"level":2}]}`, []string{"interests[0].level"}},
		{"rows as the contract names them", competenceProfileRequestSchema, "items",
			`{"key":"x","names":{"en":"X","nb":"X"},"items":[{"competenceId":1,"minLevel":2,"mustBeValid":false}]}`, nil},
		{"no list", competenceProfileRequestSchema, "items", `{"key":"x"}`, nil},
		{"a null list", competenceProfileRequestSchema, "items", `{"key":"x","items":null}`, nil},
		{"a list that is not an array", competenceProfileRequestSchema, "items", `{"key":"x","items":"x"}`, nil},
	} {
		ctx := context.WithValue(context.Background(), rawBodyKey{}, []byte(tc.body))
		f := s.unknownRowProperties(ctx, tc.schema, tc.list)
		var got []string
		for field := range f.errs {
			got = append(got, field)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: fields = %v, want %v", tc.name, got, tc.want)
		}
	}
}

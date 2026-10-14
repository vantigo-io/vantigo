package staff

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// values is a row's fields as an event names them, from literal JSON.
func values(t *testing.T, kv ...string) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}
	return out
}

// stored is a payload as the events table keeps it.
func stored(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The types this file's events name, by id: 1001 ferie and 1003 sykmelding
// show to a manager; 1005 svangerskapspermisjon is HR-only. 9999 is a type
// the lookup does not know.
var testTypes = typeVisibilities{1001: "absent", 1003: "absent", 1005: "hr_only"}

// An absence event carries its classes per field (plan pre-flight B1): the
// dates, the percent, the hours and the status C; the note D; the type C on
// a ferie row and E with the degree on a sick row. payload_classes is their
// union. None of these names enters fieldClasses.
func TestAbsenceEvent_ClassesPerField(t *testing.T) {
	t.Parallel()
	ferie := values(t, "type", "1001", "firstDay", `"2026-06-08"`, "lastDay", `"2026-06-12"`, "status", `"requested"`, "percent", "100", "note", `"Sommer"`)
	payload, classes := absenceEventPayload(7, nil, ferie, false)
	if payload["absenceId"] != int64(7) {
		t.Errorf("absenceId = %v, want 7", payload["absenceId"])
	}
	want := map[string]string{"type": "C", "firstDay": "C", "lastDay": "C", "status": "C", "percent": "C", "note": "D"}
	if got := payload["classes"].(map[string]string); !mapsEqual(got, want) {
		t.Errorf("ferie classes = %v, want %v", got, want)
	}
	if !slices.Equal(classes, []string{"C", "D"}) {
		t.Errorf("ferie payload_classes = %v, want [C D]", classes)
	}

	sick := values(t, "type", "1003", "firstDay", `"2026-06-08"`, "lastDay", `"2026-06-12"`, "status", `"registered"`, "degree", "50")
	payload, classes = absenceEventPayload(8, nil, sick, true)
	want = map[string]string{"type": "E", "degree": "E", "firstDay": "C", "lastDay": "C", "status": "C"}
	if got := payload["classes"].(map[string]string); !mapsEqual(got, want) {
		t.Errorf("sykmelding classes = %v, want %v", got, want)
	}
	if !slices.Equal(classes, []string{"C", "E"}) {
		t.Errorf("sykmelding payload_classes = %v, want [C E]", classes)
	}
	for name := range want {
		if _, ok := fieldClasses[name]; ok && name != "status" {
			t.Errorf("%s entered fieldClasses", name)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The erase blanks an absence event by its own map (plan pre-flight B1): a
// leaver's D erase blanks the note and keeps the dates and the type; the
// health erase blanks a sick row's type and degree and leaves a ferie row's
// type alone.
func TestAbsenceEvent_BlankedByItsOwnMap(t *testing.T) {
	t.Parallel()
	ferie, ferieClasses := absenceEventPayload(7, nil, values(t, "type", "1001", "firstDay", `"2026-06-08"`, "note", `"Sommer"`), false)
	sick, _ := absenceEventPayload(8, nil, values(t, "type", "1003", "firstDay", `"2026-06-08"`, "degree", "50"), true)
	fieldsOf := func(b []byte) map[string]any {
		var p struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p.Fields
	}

	out, rewritten, err := blankPayload(stored(t, ferie), "D", 1023)
	if err != nil || !rewritten {
		t.Fatalf("the D erase of a ferie event: rewritten %v, err %v", rewritten, err)
	}
	f := fieldsOf(out)
	if f["note"] != changed || f["type"] == changed || f["firstDay"] == changed {
		t.Errorf("after the D erase: %v, want the note blanked and the type and the date kept", f)
	}
	if _, rewritten, _ := blankPayload(stored(t, ferie), "E", 1023); rewritten {
		t.Errorf("the health erase rewrote a ferie event (classes %v)", ferieClasses)
	}

	out, rewritten, err = blankPayload(stored(t, sick), "E", 1023)
	if err != nil || !rewritten {
		t.Fatalf("the health erase of a sick event: rewritten %v, err %v", rewritten, err)
	}
	f = fieldsOf(out)
	if f["type"] != changed || f["degree"] != changed || f["firstDay"] == changed {
		t.Errorf("after the health erase: %v, want the type and the degree blanked and the date kept", f)
	}
	if _, rewritten, _ := blankPayload(stored(t, sick), "D", 1023); rewritten {
		t.Error("the D erase rewrote a sick event, which holds nothing of class D")
	}
}

// Shaping (plan pre-flight B2): every type value — before and after — is
// shaped by its own type's visibility: an HR-only type is "leave" to a
// manager and to HR without staff:personal-data, its id to the person and
// to staff:personal-data; a type the lookup does not know fails closed. The
// detail (percent, hours, note, degree) is the person's, the manager's and
// HR's.
func TestAbsenceEvent_ShapedByEachTypesVisibility(t *testing.T) {
	t.Parallel()
	correction, _ := absenceEventPayload(7, values(t, "type", "1005", "note", `"x"`), values(t, "type", "1001", "note", `"y"`), false)
	deleted, _ := absenceEventPayload(8, values(t, "type", "1005", "firstDay", `"2026-06-08"`), nil, false)
	unknown, _ := absenceEventPayload(9, nil, values(t, "type", "9999"), false)
	typeOf := func(payload map[string]any, a audience) map[string]any {
		fields := shapePayloadTypes(stored(t, payload), nil, a, testTypes)["fields"].(map[string]any)
		v, _ := fields["type"].(map[string]any)
		return v
	}
	for _, c := range []struct {
		name     string
		payload  map[string]any
		a        audience
		from, to any
	}{
		{"a correction away from HR-only, to the manager", correction, audience{Manager: true}, "leave", float64(1001)},
		{"a correction away from HR-only, to HR", correction, audience{Manage: true}, "leave", float64(1001)},
		{"a correction away from HR-only, to personal-data", correction, audience{PersonalData: true}, float64(1005), float64(1001)},
		{"a correction away from HR-only, to the person", correction, audience{Self: true}, float64(1005), float64(1001)},
		{"a deleted HR-only row, to the manager", deleted, audience{Manager: true}, "leave", nil},
		{"a deleted HR-only row, to personal-data", deleted, audience{PersonalData: true}, float64(1005), nil},
		{"a type nobody knows, to the manager", unknown, audience{Manager: true}, nil, "leave"},
	} {
		got := typeOf(c.payload, c.a)
		if got["from"] != c.from || got["to"] != c.to {
			t.Errorf("%s: type = %v, want from %v to %v", c.name, got, c.from, c.to)
		}
	}
	manager := shapePayloadTypes(stored(t, correction), nil, audience{Manager: true}, testTypes)
	if manager["absenceId"] != int64(7) {
		t.Errorf("absenceId = %v, want 7", manager["absenceId"])
	}
	if note, ok := manager["fields"].(map[string]any)["note"].(map[string]any); !ok || note["to"] != "y" {
		t.Errorf("the manager reads the note as %v, want its value", manager["fields"].(map[string]any)["note"])
	}
}

// An adjustment's event: the days, the year, the reason and the year it
// comes from are the account's readers'; its note is HR's, left out for
// anyone else; its classes are C and the note D.
func TestAdjustmentEvent(t *testing.T) {
	t.Parallel()
	payload, classes := adjustmentEventPayload(5, nil, values(t, "year", "2026", "days", "3", "reason", `"forced_carry_over"`, "fromYear", "2025", "note", `"Syk i ferien"`))
	if !slices.Equal(classes, []string{"C", "D"}) {
		t.Errorf("payload_classes = %v, want [C D]", classes)
	}
	manager := shapePayloadTypes(stored(t, payload), classes, audience{Manager: true}, nil)
	fields := manager["fields"].(map[string]any)
	if _, ok := fields["note"]; ok {
		t.Errorf("a manager reads the adjustment's note: %v", fields)
	}
	if days, ok := fields["days"].(map[string]any); !ok || days["to"] != float64(3) {
		t.Errorf("a manager reads days as %v, want 3", fields["days"])
	}
	if manager["adjustmentId"] != int64(5) {
		t.Errorf("adjustmentId = %v, want 5", manager["adjustmentId"])
	}
	hr := shapePayloadTypes(stored(t, payload), classes, audience{Manage: true}, nil)["fields"].(map[string]any)
	if _, ok := hr["note"].(map[string]any); !ok {
		t.Errorf("HR reads the note as %v, want its value", hr["note"])
	}
	out, rewritten, err := blankPayload(stored(t, payload), "D", 1023)
	if err != nil || !rewritten {
		t.Fatalf("D erase: rewritten %v err %v", rewritten, err)
	}
	var p struct {
		Fields map[string]any `json:"fields"`
	}
	_ = json.Unmarshal(out, &p)
	if p.Fields["note"] != changed || p.Fields["days"] == changed {
		t.Errorf("after the D erase: %v, want the note blanked and the days kept", p.Fields)
	}
}

// A field an event's own map does not class is blanked by every erase, and an
// absence event carrying no map at all is blanked whole (fail closed, review
// L1): what the module cannot place is erased rather than kept.
func TestAbsenceEvent_UnclassedFieldsBlankedByEveryErase(t *testing.T) {
	t.Parallel()
	partial := []byte(`{"absenceId":7,"fields":{"firstDay":{"to":"2026-06-08"},"mystery":{"to":"x"}},"classes":{"firstDay":"C"}}`)
	bare := []byte(`{"absenceId":8,"fields":{"firstDay":{"to":"2026-06-08"},"type":{"to":1001}}}`)
	fieldsAfter := func(stored []byte, class string) map[string]any {
		out, _, err := blankPayload(stored, class, 1023)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatal(err)
		}
		return p.Fields
	}
	for _, class := range []string{"D", "E", "A"} {
		f := fieldsAfter(partial, class)
		if f["mystery"] != changed || f["firstDay"] == changed {
			t.Errorf("the %s erase of a partly classed event: %v, want mystery blanked and firstDay kept", class, f)
		}
		f = fieldsAfter(bare, class)
		if f["firstDay"] != changed || f["type"] != changed {
			t.Errorf("the %s erase of an event without a map: %v, want every field blanked", class, f)
		}
	}
}

// Every key an absence event's payload can carry is classed on purpose, and
// a name the module does not know is E — erased by every erase, shaped by
// the health group — so a field added later fails closed (whole-branch
// review).
func TestAbsenceFieldClass_EveryKeyAndFailClosed(t *testing.T) {
	want := map[string][2]string{ // name: {class on a plain row, on a health row}
		"firstDay": {"C", "C"}, "lastDay": {"C", "C"}, "status": {"C", "C"}, "percent": {"C", "C"},
		"hours": {"C", "C"}, "selfApproved": {"C", "C"}, fieldType: {"C", "E"}, fieldDegree: {"C", "E"},
		"note": {"D", "D"}, "decisionNote": {"D", "D"}, fieldDemandedOn: {"E", "E"}, fieldPostponedUnder: {"E", "E"},
		"somethingNew": {"E", "E"},
	}
	note, decision := "n", "d"
	full := store.StaffAbsence{TypeID: 1, Hours: numericOfHundredths(200), DegreePercent: numericOfHundredths(5000),
		Percent: numericOfHundredths(wholeDay), Note: &note, DecisionNote: &decision, SelfApproved: true}
	for name := range absenceValues(full) {
		if _, ok := want[name]; !ok {
			t.Errorf("absenceValues carries %q, which this table does not class", name)
		}
	}
	for name, classes := range want {
		if got := absenceFieldClass(name, false); got != classes[0] {
			t.Errorf("absenceFieldClass(%q, plain) = %s, want %s", name, got, classes[0])
		}
		if got := absenceFieldClass(name, true); got != classes[1] {
			t.Errorf("absenceFieldClass(%q, health) = %s, want %s", name, got, classes[1])
		}
	}
}

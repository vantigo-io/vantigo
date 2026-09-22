package customers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This file tests (*brregClient).entity, brreg_entity.go's one export: the
// Brreg client fetching one entity's full record, as opposed to brreg.go's
// search. It is package customers (the brreg_internal_test.go convention),
// so it constructs a *brregClient directly with newBrregClient and drives it
// against a local httptest.Server — no HTTP handler, no modtest harness, no
// database, exactly as global-constraints.md asks ("against httptest, exactly
// as brreg_test.go does today" — that file happens to go through the
// harness's handler too, but nothing about entity requires a handler, since
// it is not itself an operation yet; Task 2 wires it to one).

// entityServer starts a local httptest server whose handler decides every
// response, and returns a *brregClient wired to it with a zero backoff (no
// test here waits out a real retry) and the standard 15s production timeout
// (ample for a loopback server). t.Cleanup closes the server.
func entityServer(t *testing.T, handler http.HandlerFunc) *brregClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newBrregClient(srv.URL, 15*time.Second, nil, zeroBackoffEntity)
}

// zeroBackoffEntity stands in for the jittered production backoff, the same
// role brreg_test.go's zeroBackoff plays for the search — a distinct name
// only because this file cannot see that one (different test binary
// package).
func zeroBackoffEntity(int) time.Duration { return 0 }

// jsonEntityResponse writes body as status with the given content type,
// defaulting to the v2 media type when contentType is "".
func jsonEntityResponse(w http.ResponseWriter, status int, contentType, body string) {
	if contentType == "" {
		contentType = brregEntityMediaType
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// equinorEntityBody is a full record with the always-present fields, a
// domestic business address and a domestic postal address of a different
// street, and the optional fields the brief pins: form ASA, industry
// 06.100, employees 21272, VAT true, founded and website set.
const equinorEntityBody = `{
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA",
	"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
	"registreringsdatoEnhetsregisteret": "1995-01-01",
	"registrertIMvaregisteret": true,
	"naeringskode1": {"kode": "06.100", "beskrivelse": "Utvinning av råolje"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 21272,
	"stiftelsesdato": "1972-06-14",
	"hjemmeside": "www.equinor.com",
	"forretningsadresse": {
		"land": "Norge",
		"landkode": "NO",
		"postnummer": "4035",
		"poststed": "STAVANGER",
		"adresse": ["Forusbeen 50"],
		"kommune": "STAVANGER",
		"kommunenummer": "1103"
	},
	"postadresse": {
		"land": "Norge",
		"landkode": "NO",
		"postnummer": "4035",
		"poststed": "STAVANGER",
		"adresse": ["Postboks 8500"],
		"kommune": "STAVANGER",
		"kommunenummer": "1103"
	},
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false,
	"maalform": "Bokmål",
	"registrertIForetaksregisteret": true,
	"registrertIFrivillighetsregisteret": false,
	"erIKonsern": true
}`

// brregSelfEntityBody is the verified live body for Registerenheten i
// Brønnøysund (org 974760673), quoted verbatim (fix round 1: the first
// version of this fixture had invented epostadresse/telefon values and an
// unverified mobil that the live registry does not send). Its own
// respons_klasse is "Enhet", not "SlettetEnhet" — present, but not the
// deleted marker — which doubles as coverage that a present-but-different
// respons_klasse is still an ordinary found entity.
const brregSelfEntityBody = `{"organisasjonsnummer":"974760673","navn":"REGISTERENHETEN I BRØNNØYSUND","organisasjonsform":{"kode":"ORGL","beskrivelse":"Organisasjonsledd"},"hjemmeside":"www.brreg.no","postadresse":{"land":"Norge","landkode":"NO","postnummer":"8910","poststed":"BRØNNØYSUND","adresse":["Postboks 900"],"kommune":"BRØNNØY","kommunenummer":"1813"},"registreringsdatoEnhetsregisteret":"1995-08-09","registrertIMvaregisteret":false,"naeringskode1":{"kode":"84.110","beskrivelse":"Generell offentlig administrasjon"},"antallAnsatte":487,"harRegistrertAntallAnsatte":true,"overordnetEnhet":"912660680","epostadresse":"firmapost@brreg.no","telefon":"75 00 75 09","forretningsadresse":{"land":"Norge","landkode":"NO","postnummer":"8900","poststed":"BRØNNØYSUND","adresse":["Havnegata 48"],"kommune":"BRØNNØY","kommunenummer":"1813"},"institusjonellSektorkode":{"kode":"6100","beskrivelse":"Statsforvaltningen"},"registrertIForetaksregisteret":false,"registrertIStiftelsesregisteret":false,"registrertIFrivillighetsregisteret":false,"konkurs":false,"underAvvikling":false,"underTvangsavviklingEllerTvangsopplosning":false,"maalform":"Bokmål","aktivitet":["Registeretat."],"registrertIPartiregisteret":false,"paategninger":[],"erIKonsern":false,"respons_klasse":"Enhet"}`

// slettetEnhetBody is a deleted entity: HTTP 200, respons_klasse
// "SlettetEnhet", and the reduced field set the research found — only
// organisasjonsnummer, navn, organisasjonsform, historiskeNavn and
// slettedato. organisasjonsform is deliberately included here to prove the
// deleted outcome does NOT populate OrganisationFormCode/OrganisationForm on
// the record (only Name/OrganisationNumber/DeletedOn do, per the brief).
const slettetEnhetBody = `{
	"respons_klasse": "SlettetEnhet",
	"organisasjonsnummer": "923609016",
	"navn": "SLETTET SELSKAP AS",
	"organisasjonsform": {"kode": "AS", "beskrivelse": "Aksjeselskap"},
	"historiskeNavn": [{"navn": "GAMMELT NAVN AS", "fraDato": "2010-01-01 00:00:00"}],
	"slettedato": "2026-09-21"
}`

// foreignAddressEntityBody wraps the research's exact foreign-address body
// (Polish, no postnummer/kommune) as forretningsadresse on an otherwise
// minimal entity, so the address-mapping assertions have a real record to
// decode it from.
const foreignAddressEntityBody = `{
	"organisasjonsnummer": "999999999",
	"navn": "UTENLANDSK FILIAL",
	"organisasjonsform": {"kode": "NUF", "beskrivelse": "Norskregistrert utenlandsk foretak"},
	"harRegistrertAntallAnsatte": false,
	"registrertIMvaregisteret": false,
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false,
	"forretningsadresse": {"land": "Polen", "landkode": "PL", "poststed": "81-336 GDYNIA", "adresse": ["ul. Budowniczych 12"]}
}`

func mustEntity(t *testing.T, c *brregClient, orgnr string) (brregEntityRecord, brregEntityOutcome) {
	t.Helper()
	rec, outcome, err := c.entity(t.Context(), orgnr)
	if err != nil {
		t.Fatalf("entity(%q) error = %v, want nil", orgnr, err)
	}
	return rec, outcome
}

func intPtr32(v int32) *int32 { return &v }

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("mustDate(%q): %v", s, err)
	}
	return d
}

// TestEntity_FullRecordEveryField pins the Equinor and Brønnøysundregistrene
// bodies' full field mapping, one struct literal each, so a change to any
// single field's source key or transform breaks exactly one test.
func TestEntity_FullRecordEveryField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		orgnr string
		body  string
		want  brregEntityRecord
	}{
		{
			name:  "Equinor",
			orgnr: "923609016",
			body:  equinorEntityBody,
			want: brregEntityRecord{
				OrganisationNumber:   "923609016",
				Name:                 "EQUINOR ASA",
				OrganisationFormCode: "ASA",
				OrganisationForm:     "Allmennaksjeselskap",
				IndustryCode:         "06.100",
				Industry:             "Utvinning av råolje",
				Employees:            intPtr32(21272),
				VATRegistered:        true,
				FoundedOn:            timePtr(mustDate(t, "1972-06-14")),
				Website:              "www.equinor.com",
				BusinessAddress: &brregAddress{
					Lines:        []string{"Forusbeen 50"},
					PostalCode:   "4035",
					City:         "STAVANGER",
					Municipality: "STAVANGER",
					CountryCode:  "NO",
				},
				PostalAddress: &brregAddress{
					Lines:        []string{"Postboks 8500"},
					PostalCode:   "4035",
					City:         "STAVANGER",
					Municipality: "STAVANGER",
					CountryCode:  "NO",
				},
			},
		},
		{
			name:  "RegisterenhetenIBronnoysund",
			orgnr: "974760673",
			body:  brregSelfEntityBody,
			want: brregEntityRecord{
				OrganisationNumber:       "974760673",
				Name:                     "REGISTERENHETEN I BRØNNØYSUND",
				OrganisationFormCode:     "ORGL",
				OrganisationForm:         "Organisasjonsledd",
				IndustryCode:             "84.110",
				Industry:                 "Generell offentlig administrasjon",
				Employees:                intPtr32(487),
				VATRegistered:            false,
				Website:                  "www.brreg.no",
				Email:                    "firmapost@brreg.no",
				Phone:                    "75 00 75 09",
				ParentOrganisationNumber: "912660680",
				BusinessAddress: &brregAddress{
					Lines:        []string{"Havnegata 48"},
					PostalCode:   "8900",
					City:         "BRØNNØYSUND",
					Municipality: "BRØNNØY",
					CountryCode:  "NO",
				},
				PostalAddress: &brregAddress{
					Lines:        []string{"Postboks 900"},
					PostalCode:   "8910",
					City:         "BRØNNØYSUND",
					Municipality: "BRØNNØY",
					CountryCode:  "NO",
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
				jsonEntityResponse(w, http.StatusOK, "", tc.body)
			})
			rec, outcome := mustEntity(t, c, tc.orgnr)
			if outcome != brregEntityFound {
				t.Fatalf("outcome = %v, want brregEntityFound", outcome)
			}
			// reflect.DeepEqual dereferences the *int32/*time.Time/*brregAddress
			// pointer fields and compares what they point to, so this catches a
			// wrong value behind a pointer, not just a nil/non-nil mismatch.
			if !reflect.DeepEqual(rec, tc.want) {
				t.Errorf("entity(%q) record =\n  %+v\nwant\n  %+v", tc.orgnr, rec, tc.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// TestEntity_ForeignAddressHasNoPostalCodeOrMunicipality pins the research's
// exact foreign-address body: no postnummer/kommune on a foreign address
// (the post code lives inside poststed instead), landkode already
// upper-case.
func TestEntity_ForeignAddressHasNoPostalCodeOrMunicipality(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", foreignAddressEntityBody)
	})
	rec, outcome := mustEntity(t, c, "999999999")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.BusinessAddress == nil {
		t.Fatalf("BusinessAddress = nil, want set")
	}
	want := brregAddress{
		Lines:        []string{"ul. Budowniczych 12"},
		PostalCode:   "",
		City:         "81-336 GDYNIA",
		Municipality: "",
		CountryCode:  "PL",
	}
	got := *rec.BusinessAddress
	if got.PostalCode != want.PostalCode || got.City != want.City || got.Municipality != want.Municipality || got.CountryCode != want.CountryCode || strings.Join(got.Lines, "|") != strings.Join(want.Lines, "|") {
		t.Errorf("BusinessAddress = %+v, want %+v", got, want)
	}
	if rec.Employees != nil {
		t.Errorf("Employees = %v, want nil (harRegistrertAntallAnsatte is false)", *rec.Employees)
	}
}

// TestEntity_MobileMapsFromMobilField pins the Mobile field on its own
// (fix round 1: neither live fixture carries a "mobil" — the verified
// Registerenheten i Brønnøysund body has none — so this is a small
// synthetic body dedicated to proving that field's mapping is still wired).
func TestEntity_MobileMapsFromMobilField(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": true,
		"mobil": "90758800"
	}`
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", body)
	})
	rec, outcome := mustEntity(t, c, "923609016")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.Mobile != "90758800" {
		t.Errorf("Mobile = %q, want %q", rec.Mobile, "90758800")
	}
}

// TestEntity_EmployeesStaysNilWhenNotRegisteredEvenIfCountPresent is a
// defensive case the live registry does not itself produce (the research
// note: antallAnsatte is absent whenever harRegistrertAntallAnsatte is
// false) — but the rule is "set only when harRegistrertAntallAnsatte is
// true AND antallAnsatte is present", a conjunction, not just "antallAnsatte
// is present". This is the only fixture where the two disagree, so it is
// the only one that can catch a mapping that dropped the flag check.
func TestEntity_EmployeesStaysNilWhenNotRegisteredEvenIfCountPresent(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"antallAnsatte": 50,
		"registrertIMvaregisteret": true
	}`
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", body)
	})
	rec, outcome := mustEntity(t, c, "923609016")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.Employees != nil {
		t.Errorf("Employees = %v, want nil (harRegistrertAntallAnsatte is false even though antallAnsatte was sent)", *rec.Employees)
	}
}

// TestEntity_AddressLinesAreTrimmedAndEmptyLinesDropped and
// TestEntity_LandkodeIsUppercasedOrEmpty exercise brregAddressFrom directly
// (a pure unexported helper), the fastest way to pin the trimming/dropping
// and landkode rules without routing every case through an HTTP fixture.
func TestEntity_AddressLinesAreTrimmedAndEmptyLinesDropped(t *testing.T) {
	t.Parallel()
	wire := &brregAddressWire{Adresse: []string{"  Storgata 1  ", "", "   ", "c/o Someone"}}
	got := brregAddressFrom(wire)
	if got == nil {
		t.Fatalf("brregAddressFrom = nil, want set")
	}
	want := []string{"Storgata 1", "c/o Someone"}
	if strings.Join(got.Lines, "|") != strings.Join(want, "|") {
		t.Errorf("Lines = %v, want %v", got.Lines, want)
	}
}

func TestEntity_LandkodeIsUppercasedOrEmpty(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"NO":  "NO",
		"no":  "NO",
		"gb":  "GB",
		"":    "",
		"NOR": "",
		"N":   "",
	}
	for in, want := range cases {
		got := brregAddressFrom(&brregAddressWire{Landkode: in})
		if got.CountryCode != want {
			t.Errorf("brregAddressFrom(landkode=%q).CountryCode = %q, want %q", in, got.CountryCode, want)
		}
	}
}

// TestEntity_NilAddressStaysNil proves an absent forretningsadresse/postadresse
// decodes to a nil *brregAddress, never a zero-valued one a caller could
// mistake for "an address with every field blank".
func TestEntity_NilAddressStaysNil(t *testing.T) {
	t.Parallel()
	if got := brregAddressFrom(nil); got != nil {
		t.Errorf("brregAddressFrom(nil) = %+v, want nil", got)
	}
}

// TestEntity_StatusFlagsMapDirectly proves konkurs/underAvvikling/
// underTvangsavviklingEllerTvangsopplosning each map to their own field —
// Equinor and Brønnøysundregistrene's fixtures are all false for these three,
// so nothing else in this file would catch a mutation that hardcoded them.
func TestEntity_StatusFlagsMapDirectly(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "111111111",
		"navn": "SELSKAP UNDER AVVIKLING AS",
		"organisasjonsform": {"kode": "AS", "beskrivelse": "Aksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": false,
		"konkurs": true,
		"underAvvikling": true,
		"underTvangsavviklingEllerTvangsopplosning": true
	}`
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", body)
	})
	rec, outcome := mustEntity(t, c, "111111111")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if !rec.Bankrupt || !rec.UnderLiquidation || !rec.UnderForcedLiquidation {
		t.Errorf("Bankrupt=%v UnderLiquidation=%v UnderForcedLiquidation=%v, want all true", rec.Bankrupt, rec.UnderLiquidation, rec.UnderForcedLiquidation)
	}
}

// TestEntity_StatusDatesMapFromKonkursdatoAndUnderAvviklingDato pins the two
// dates the registry sends beside the flags (fix round 2, I3): when a company
// went bankrupt and when it went into liquidation, which is what the
// dashboard's attention item is dated by — a 2019 bankruptcy must not read as
// having happened on the day it was last fetched. Both are optional, exactly
// as stiftelsesdato is.
func TestEntity_StatusDatesMapFromKonkursdatoAndUnderAvviklingDato(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "111111111",
		"navn": "KONKURS AS",
		"organisasjonsform": {"kode": "AS", "beskrivelse": "Aksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": false,
		"konkurs": true,
		"konkursdato": "2019-03-04",
		"underAvvikling": true,
		"underAvviklingDato": "2018-11-30",
		"underTvangsavviklingEllerTvangsopplosning": false
	}`
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", body)
	})
	rec, outcome := mustEntity(t, c, "111111111")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.BankruptOn == nil || !rec.BankruptOn.Equal(mustDate(t, "2019-03-04")) {
		t.Errorf("BankruptOn = %v, want 2019-03-04", rec.BankruptOn)
	}
	if rec.LiquidationOn == nil || !rec.LiquidationOn.Equal(mustDate(t, "2018-11-30")) {
		t.Errorf("LiquidationOn = %v, want 2018-11-30", rec.LiquidationOn)
	}
}

// TestEntity_StatusDatesAreOptional is the other half: the flags are the
// load-bearing facts, and a body carrying them without a date leaves both
// dates nil rather than failing.
func TestEntity_StatusDatesAreOptional(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	rec, _ := mustEntity(t, c, "923609016")
	if rec.BankruptOn != nil || rec.LiquidationOn != nil {
		t.Errorf("BankruptOn/LiquidationOn = %v/%v, want nil/nil", rec.BankruptOn, rec.LiquidationOn)
	}
}

// TestEntity_OversizedBodyIsNotRetried pins the oversized body as a terminal
// failure (fix round 2, minors): a registry answering a megabyte of nonsense
// will answer the same megabyte three more times, so the retry budget a
// genuine outage needs is not spent on it. The body-read failure beside it is
// terminal for the same reason.
func TestEntity_OversizedBodyIsNotRetried(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	oversized := strings.Repeat("a", brregEntityMaxBodyBytes+1)
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		jsonEntityResponse(w, http.StatusOK, "", oversized)
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for an oversized body")
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want exactly 1 (an oversized body is never retried)", got)
	}
}

// TestEntity_OrganisationNumberMismatchIsAnError pins the one thing a
// response must agree with the request about (fix round 2, minors): the
// organisation number asked for. A body about a different company — a
// misrouted proxy, a cache serving the wrong key — must never be stored as
// this customer's record, so it is a terminal error, the same shape a
// malformed body is.
func TestEntity_OrganisationNumberMismatchIsAnError(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	_, _, err := c.entity(t.Context(), "974760673")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a body about another organisation")
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want exactly 1 (a mismatch is not something a retry fixes)", got)
	}
}

// TestEntity_DeletedEntityIsA200Body pins the registry's own quirk: a
// deleted entity is HTTP 200 with respons_klasse "SlettetEnhet", branched on
// the body, not the status. Only Name/OrganisationNumber/DeletedOn are
// filled — OrganisationFormCode stays "" even though the body's
// organisasjonsform is present, because the brief's outcome contract names
// exactly those three fields.
func TestEntity_DeletedEntityIsA200Body(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", slettetEnhetBody)
	})
	rec, outcome, err := c.entity(t.Context(), "923609016")
	if err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	if outcome != brregEntityDeleted {
		t.Fatalf("outcome = %v, want brregEntityDeleted", outcome)
	}
	if rec.OrganisationNumber != "923609016" {
		t.Errorf("OrganisationNumber = %q, want %q", rec.OrganisationNumber, "923609016")
	}
	if rec.Name != "SLETTET SELSKAP AS" {
		t.Errorf("Name = %q, want %q", rec.Name, "SLETTET SELSKAP AS")
	}
	if rec.DeletedOn == nil || !rec.DeletedOn.Equal(mustDate(t, "2026-09-21")) {
		t.Errorf("DeletedOn = %v, want 2026-09-21", rec.DeletedOn)
	}
	if rec.OrganisationFormCode != "" {
		t.Errorf("OrganisationFormCode = %q, want empty (not part of the deleted outcome's fields)", rec.OrganisationFormCode)
	}
}

// TestEntity_UnknownOn404 pins the 404-empty-body case: unknown, no error,
// and — since 404 is not a retryable status — exactly one attempt.
func TestEntity_UnknownOn404(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})
	rec, outcome, err := c.entity(t.Context(), "000000000")
	if err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	if outcome != brregEntityUnknown {
		t.Fatalf("outcome = %v, want brregEntityUnknown", outcome)
	}
	if rec != (brregEntityRecord{}) {
		t.Errorf("record = %+v, want zero value", rec)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a 404 is never retried)", got)
	}
}

// TestEntity_RemovedOn410 pins the "removed from open data" outcome: HTTP
// 410 with a slettedato in the body, never retried.
func TestEntity_RemovedOn410(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		jsonEntityResponse(w, http.StatusGone, "application/json",
			`{"organisasjonsnummer":"923609016","slettedato":"2026-09-21","_links":{"self":{"href":"x"}}}`)
	})
	rec, outcome, err := c.entity(t.Context(), "923609016")
	if err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	if outcome != brregEntityRemoved {
		t.Fatalf("outcome = %v, want brregEntityRemoved", outcome)
	}
	if rec.DeletedOn == nil || !rec.DeletedOn.Equal(mustDate(t, "2026-09-21")) {
		t.Errorf("DeletedOn = %v, want 2026-09-21", rec.DeletedOn)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a 410 is never retried)", got)
	}
}

// TestEntity_RetriesTransientFailureThenSucceeds proves entity() reuses the
// search's retry/backoff machinery: a 500 on the first attempt, a full
// Equinor body on the second, found with exactly two attempts, the backoff
// seam (zeroBackoffEntity) never actually sleeping.
func TestEntity_RetriesTransientFailureThenSucceeds(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	rec, outcome := mustEntity(t, c, "923609016")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.Name != "EQUINOR ASA" {
		t.Errorf("Name = %q, want EQUINOR ASA", rec.Name)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (one failure then a success)", got)
	}
}

// TestEntity_WrongMediaTypeIsAnError pins the Accept/Content-Type contract:
// a 406 (the shape a caller gets from asking for a media type the server no
// longer serves) whose Content-Type does not parse to the v2 type or
// application/json is an "unavailable" error naming the content type it
// got, not a panic or a silent empty record.
func TestEntity_WrongMediaTypeIsAnError(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusNotAcceptable, "application/problem+json", `{"title":"not acceptable"}`)
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a 406 with a non-matching content type")
	}
	if !strings.Contains(err.Error(), "application/problem+json") {
		t.Errorf("error = %v, want it to name the content type actually received", err)
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
}

// TestEntity_HTMLContentTypeIsAnError is TestEntity_WrongMediaTypeIsAnError's
// other named example (this file's doc comment on
// validateBrregEntityContentType): a 200 whose Content-Type is text/html —
// the shape a misbehaving proxy's error page would carry — is refused the
// same way a wrong-typed 406 is, naming the content type it actually got.
func TestEntity_HTMLContentTypeIsAnError(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "text/html", "<html><body>not json</body></html>")
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a 200 with Content-Type text/html")
	}
	if !strings.Contains(err.Error(), "text/html") {
		t.Errorf("error = %v, want it to name the content type actually received", err)
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
}

// TestEntity_JSONContentTypeWithCharsetIsAccepted proves
// validateBrregEntityContentType's use of mime.ParseMediaType, not a plain
// string comparison: "application/json; charset=utf-8" still parses to the
// bare media type "application/json", which this operation already accepts
// alongside the pinned v2 type.
func TestEntity_JSONContentTypeWithCharsetIsAccepted(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "application/json; charset=utf-8", equinorEntityBody)
	})
	rec, outcome := mustEntity(t, c, "923609016")
	if outcome != brregEntityFound {
		t.Fatalf("outcome = %v, want brregEntityFound", outcome)
	}
	if rec.Name != "EQUINOR ASA" {
		t.Errorf("Name = %q, want EQUINOR ASA", rec.Name)
	}
}

// TestEntity_OversizedBodyIsAnError proves the end-to-end behaviour a caller
// actually sees for a 2 MiB body: an error, wrapping errBrregUnavailable.
// It does not by itself pin *how* the cap is enforced — entityAttempt's own
// io.LimitReader already stops reading at brregEntityMaxBodyBytes+1 bytes,
// so a body past that point never reaches json.Unmarshal intact and would
// fail to decode as truncated JSON even without the explicit length check
// below; TestEntityAttempt_RefusesBodyOverOneMiB and
// TestEntityAttempt_AcceptsBodyExactlyAtCap pin that check directly, at the
// byte boundary, independent of JSON validity.
func TestEntity_OversizedBodyIsAnError(t *testing.T) {
	t.Parallel()
	oversized := strings.Repeat("a", 2*1024*1024)
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", oversized)
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a 2 MiB body")
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
}

// TestEntityAttempt_RefusesBodyOverOneMiB and
// TestEntityAttempt_AcceptsBodyExactlyAtCap pin brregEntityMaxBodyBytes's
// exact boundary directly against entityAttempt (bypassing JSON decoding
// entirely, so these do not depend on how a truncated body happens to fail
// to parse): one byte over the cap is refused with an error naming the
// cap, exactly at the cap is accepted with the whole body intact.
func TestEntityAttempt_RefusesBodyOverOneMiB(t *testing.T) {
	t.Parallel()
	overCap := strings.Repeat("a", brregEntityMaxBodyBytes+1)
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", overCap)
	})
	_, _, _, err := c.entityAttempt(t.Context(), brregEntityPath("923609016"))
	if err == nil {
		t.Fatal("entityAttempt error = nil, want an error for a body one byte over the cap")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error = %v, want it to name the cap being exceeded", err)
	}
}

func TestEntityAttempt_AcceptsBodyExactlyAtCap(t *testing.T) {
	t.Parallel()
	atCap := strings.Repeat("a", brregEntityMaxBodyBytes)
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", atCap)
	})
	_, _, body, err := c.entityAttempt(t.Context(), brregEntityPath("923609016"))
	if err != nil {
		t.Fatalf("entityAttempt error = %v, want nil for a body exactly at the cap", err)
	}
	if len(body) != brregEntityMaxBodyBytes {
		t.Errorf("len(body) = %d, want %d (the whole body, intact)", len(body), brregEntityMaxBodyBytes)
	}
}

// TestEntity_MalformedJSONIsAnError pins a malformed body on an otherwise
// successful, correctly-typed response as an error: the registry never
// sends invalid JSON, so this can only be a bug or a hostile proxy, and
// either way there is nothing sensible to return.
func TestEntity_MalformedJSONIsAnError(t *testing.T) {
	t.Parallel()
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", "not json")
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a malformed body")
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
}

// TestEntity_MalformedDateIsAnError pins the dates rule: the registry never
// sends a malformed date, so one is treated as a malformed body — an error,
// not a nil date silently swallowing the problem.
func TestEntity_MalformedDateIsAnError(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": true,
		"stiftelsesdato": "14-06-1972"
	}`
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonEntityResponse(w, http.StatusOK, "", body)
	})
	_, _, err := c.entity(t.Context(), "923609016")
	if err == nil {
		t.Fatal("entity() error = nil, want an error for a malformed date")
	}
	if !errors.Is(err, errBrregUnavailable) {
		t.Errorf("error = %v, want it to wrap errBrregUnavailable", err)
	}
}

// TestEntity_AcceptHeaderIsPinnedV2MediaType proves the request always
// asks for the pinned v2 media type — the header the registry's own 406
// (v1 is gone) is why this whole file's decoding assumes it got one back.
func TestEntity_AcceptHeaderIsPinnedV2MediaType(t *testing.T) {
	t.Parallel()
	var gotAccept atomic.Value
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAccept.Store(r.Header.Get("Accept"))
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	if _, _, err := c.entity(t.Context(), "923609016"); err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	if got, _ := gotAccept.Load().(string); got != brregEntityMediaType {
		t.Errorf("Accept header = %q, want %q", got, brregEntityMediaType)
	}
}

// TestEntity_AcceptHeaderIsSentOnEveryRetriedAttempt proves the header
// survives a retry, not only the first attempt.
func TestEntity_AcceptHeaderIsSentOnEveryRetriedAttempt(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	var wrongHeaderSeen atomic.Bool
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != brregEntityMediaType {
			wrongHeaderSeen.Store(true)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	if _, _, err := c.entity(t.Context(), "923609016"); err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if wrongHeaderSeen.Load() {
		t.Error("some attempt did not send the pinned v2 Accept header")
	}
}

// TestEntity_RequestPathIsEnhetsregisteretEnheterOrgnr pins the exact path
// this operation hits, as distinct from brregPath's own search usage.
func TestEntity_RequestPathIsEnhetsregisteretEnheterOrgnr(t *testing.T) {
	t.Parallel()
	var gotPath atomic.Value
	c := entityServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		jsonEntityResponse(w, http.StatusOK, "", equinorEntityBody)
	})
	if _, _, err := c.entity(t.Context(), "923609016"); err != nil {
		t.Fatalf("entity() error = %v, want nil", err)
	}
	want := "/enhetsregisteret/api/enheter/923609016"
	if got, _ := gotPath.Load().(string); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

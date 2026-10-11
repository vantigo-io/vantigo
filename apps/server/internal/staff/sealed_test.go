package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func nationalIDPath(id int64) string        { return personPath(id) + "/national-id" }
func nationalIDRevealPath(id int64) string  { return nationalIDPath(id) + "/reveal" }
func bankAccountPath(id int64) string       { return personPath(id) + "/bank-account" }
func bankAccountRevealPath(id int64) string { return bankAccountPath(id) + "/reveal" }

// testNationalID is an eleven-digit fødselsnummer born on birth (DDMMYY) with
// the first individual number from individual up whose two control digits
// exist — ident's test helper, copied because it is another package's. The
// month is in Skatteetaten's synthetic range (plus 80), so no number built
// here is a real person's; an individual number from 0 to 499 with a year
// of 90 is a birth in 1990.
func testNationalID(birth string, individual int) string {
	first := []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	second := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	check := func(digits, weights []int) int {
		sum := 0
		for i, w := range weights {
			sum += digits[i] * w
		}
		return (11 - sum%11) % 11
	}
	for n := individual; n < 1000; n++ {
		digits := make([]int, 0, 11)
		for _, r := range fmt.Sprintf("%s%03d", birth, n) {
			digits = append(digits, int(r-'0'))
		}
		c1 := check(digits, first)
		if c1 == 10 {
			continue
		}
		digits = append(digits, c1)
		c2 := check(digits, second)
		if c2 == 10 {
			continue
		}
		digits = append(digits, c2)
		var b strings.Builder
		for _, d := range digits {
			b.WriteByte(byte('0' + d))
		}
		return b.String()
	}
	panic("testNationalID: no individual number gives two control digits for " + birth)
}

// leaks reports whether text carries number, or any four consecutive digits
// of its last five — the individual number and the control digits, which
// with the birth date make the whole (design S4). The birth date alone is
// the person's private group, read where it is shown, so it is not looked
// for.
func leaks(text, number string) bool {
	if strings.Contains(text, number) {
		return true
	}
	tail := number[len(number)-5:]
	for i := 0; i+4 <= len(tail); i++ {
		if strings.Contains(text, tail[i:i+4]) {
			return true
		}
	}
	return false
}

// carriesMoreThanLast4 reports whether text carries five or more consecutive
// characters of account: what the bank-change mail may never show.
func carriesMoreThanLast4(text, account string) bool {
	for i := 0; i+5 <= len(account); i++ {
		if strings.Contains(text, account[i:i+5]) {
			return true
		}
	}
	return false
}

// The national identity number in one round trip (design S4): staff:personal-data
// writes it; no response, history or log line carries a digit of it — the
// writer's own answer, the person's, HR's — only its kind and that one is on
// file; what is stored is sealed and its MAC 32 bytes; the reveal answers it
// once with Cache-Control no-store and records who looked.
func TestNationalID_SealedRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signInNamed(t, h, "Pia Personal", "staff:personal-data")
	number := testNationalID("018190", 347)
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "birthDate", "1990-01-01"))
	self, user := signIn(t, h)
	link(t, h, p.Id, user)

	r := pd.Do(http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": number})
	if r.Status != http.StatusOK {
		t.Fatalf("set: %d %s, want 200", r.Status, r.Body)
	}
	if leaks(string(r.Body), number) {
		t.Errorf("the set's answer carries the number: %s", r.Body)
	}
	for name, c := range map[string]*modtest.Client{"staff:personal-data": pd, "staff:manage": hr, "self": self} {
		got := c.Do(http.MethodGet, personPath(p.Id), nil)
		if leaks(string(got.Body), number) {
			t.Errorf("%s reads the number: %s", name, got.Body)
		}
		id, _ := raw(t, got)["nationalId"].(map[string]any)
		if len(id) != 2 || id["kind"] != "fodselsnummer" || id["onFile"] != true {
			t.Errorf("%s nationalId = %v, want the kind and onFile only", name, id)
		}
		if events := c.Do(http.MethodGet, eventsPath(p.Id), nil); leaks(string(events.Body), number) {
			t.Errorf("%s's history carries the number: %s", name, events.Body)
		}
	}
	sealed := modtest.One[[]byte](t, h.Harness, `SELECT national_id_sealed FROM staff.people WHERE id = $1`, p.Id)
	mac := modtest.One[[]byte](t, h.Harness, `SELECT national_id_mac FROM staff.people WHERE id = $1`, p.Id)
	if len(sealed) == 0 || leaks(string(sealed), number) {
		t.Errorf("national_id_sealed = %x, want a sealed value", sealed)
	}
	if len(mac) != 32 {
		t.Errorf("national_id_mac = %d bytes, want 32", len(mac))
	}

	revealed := pd.Do(http.MethodPost, nationalIDRevealPath(p.Id), nil)
	if revealed.Status != http.StatusOK {
		t.Fatalf("reveal: %d %s, want 200", revealed.Status, revealed.Body)
	}
	if cc := revealed.Header("Cache-Control"); cc != "no-store" {
		t.Errorf("reveal Cache-Control = %q, want no-store", cc)
	}
	var value gen.StaffNationalIdReveal
	revealed.JSON(&value)
	if value.Value != number || value.Kind != gen.Fodselsnummer || value.Country != nil {
		t.Errorf("revealed = %+v, want the number as a fødselsnummer", value)
	}

	events := listEvents(t, hr, p.Id)
	if events[0].Kind != "national_id_revealed" || events[1].Kind != "national_id_set" {
		t.Fatalf("latest events = %s, %s, want national_id_revealed, national_id_set", events[0].Kind, events[1].Kind)
	}
	if a := events[0].Actor; a.UserId == nil || *a.UserId != pdID || a.DisplayName != "Pia Personal" {
		t.Errorf("the reveal's actor = %+v, want the viewer", a)
	}
	if set := fieldsOf(events[1])["nationalId"].(map[string]any); set["to"].(map[string]any)["kind"] != "fodselsnummer" {
		t.Errorf("national_id_set = %v, want the kind it was set to", set)
	}
	if leaks(h.Logs(), number) {
		t.Error("a log line carries the number")
	}
}

// A refusal never echoes the number it refused: an invalid one, a body the
// server could not decode, a value of the wrong type. Nothing is written,
// and no log line carries it either.
func TestNationalID_RefusalsEchoNoDigit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	number := testNationalID("018190", 347)
	wrong := number[:10] + string('0'+(number[10]-'0'+1)%10) // the second control digit off by one

	pr := refused(t, pd.Do(http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": wrong}),
		http.StatusBadRequest, "national_id_invalid")
	if len(pr.Errors["value"]) == 0 {
		t.Errorf("errors = %v, want value named", pr.Errors)
	}
	for _, c := range []struct{ name, body string }{
		{"an invalid one", `{"kind":"fodselsnummer","value":"` + wrong + `"}`},
		{"the value unquoted", `{"kind":"fodselsnummer","value":` + number + `}`},
		{"the body cut short", `{"kind":"fodselsnummer","value":"` + number},
		{"a stray character after it", `{"kind":"fodselsnummer","value":"` + number + `"x}`},
		{"a foreign id without its country", `{"kind":"foreign","value":"` + number + `"}`},
	} {
		r := pd.Do(http.MethodPut, nationalIDPath(p.Id), nil, modtest.RawBody("application/json", []byte(c.body)))
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", c.name, r.Status, r.Body)
		}
		if leaks(string(r.Body), number) || leaks(string(r.Body), wrong) {
			t.Errorf("%s: the refusal carries the number: %s", c.name, r.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE national_id_mac IS NOT NULL`); n != 0 {
		t.Errorf("%d numbers stored, want none", n)
	}
	if leaks(h.Logs(), number) || leaks(h.Logs(), wrong) {
		t.Error("a log line carries the number")
	}
}

// The same number on two people is refused by its MAC, without opening
// anything, and the refusal names nobody — not the other person's name,
// employee number or id. Written the other way (spaces between the groups)
// it is the same number. The same digits as another country's foreign id
// are another identifier and are accepted, and setting a person's own
// number again is no duplicate of itself.
func TestNationalID_DuplicateRefusedNamingNobody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	number := testNationalID("018190", 347)
	first := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "DUP-1"))
	second := createPerson(t, hr, newPerson("Ola", "Hansen"))
	do(t, pd, http.MethodPut, nationalIDPath(first.Id), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)

	for _, value := range []string{number, number[:6] + " " + number[6:]} {
		r := pd.Do(http.MethodPut, nationalIDPath(second.Id), map[string]any{"kind": "fodselsnummer", "value": value})
		refused(t, r, http.StatusBadRequest, "national_id_duplicate")
		for _, name := range []string{"Kari", "Nordmann", "DUP-1", fmt.Sprint(first.Id)} {
			if strings.Contains(string(r.Body), name) {
				t.Errorf("the refusal names %q: %s", name, r.Body)
			}
		}
		if leaks(string(r.Body), number) {
			t.Errorf("the refusal carries the number: %s", r.Body)
		}
	}
	do(t, pd, http.MethodPut, nationalIDPath(first.Id), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)
	var out gen.StaffPerson
	do(t, pd, http.MethodPut, nationalIDPath(second.Id), map[string]any{"kind": "foreign", "country": "se", "value": number}, http.StatusOK, &out)
	if out.NationalId == nil || out.NationalId.Kind == nil || *out.NationalId.Kind != gen.Foreign || !out.NationalId.OnFile {
		t.Errorf("the foreign id = %+v, want on file as foreign", out.NationalId)
	}
	var revealed gen.StaffNationalIdReveal
	do(t, pd, http.MethodPost, nationalIDRevealPath(second.Id), nil, http.StatusOK, &revealed)
	if revealed.Country == nil || *revealed.Country != "SE" || revealed.Kind != gen.Foreign {
		t.Errorf("revealed = %+v, want a Swedish foreign id", revealed)
	}
}

// The MAC column is unique in the schema itself, so a duplicate is refused
// whatever path writes it — the race between two writers included.
func TestNationalID_TheMACIndexIsUnique(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first, second := seedPerson(t, h, "Kari", "Nordmann"), seedPerson(t, h, "Ola", "Hansen")
	h.Exec(t, `UPDATE staff.people SET national_id_mac = '\x0102'::bytea WHERE id = $1`, first)
	_, err := h.Pool().Exec(context.Background(), `UPDATE staff.people SET national_id_mac = '\x0102'::bytea WHERE id = $1`, second)
	if !db.IsUniqueViolation(err, "people_national_id_mac_key") {
		t.Errorf("a second person with the same MAC: %v, want the unique violation", err)
	}
}

// A fødselsnummer encodes a birth date; one that disagrees with the person's
// is refused (plan reading 17) and stores nothing. A person with no birth
// date, the matching one, or a foreign id whatever the date are accepted.
func TestNationalID_BirthDateMismatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	for i, c := range []struct {
		name, birthDate, kind, country string
		want                           int
		code                           string
	}{
		{"another birth date", "1990-01-02", "fodselsnummer", "", http.StatusBadRequest, "national_id_birth_date_mismatch"},
		{"the same birth date", "1990-01-01", "fodselsnummer", "", http.StatusOK, ""},
		{"no birth date", "", "fodselsnummer", "", http.StatusOK, ""},
		{"a foreign id", "1975-06-30", "foreign", "DK", http.StatusOK, ""},
	} {
		body := newPerson("Kari", fmt.Sprintf("Nordmann%d", i))
		if c.birthDate != "" {
			body = with(body, "birthDate", c.birthDate)
		}
		p := createPerson(t, hr, body)
		number := testNationalID("018190", 100+100*i)
		request := map[string]any{"kind": c.kind, "value": number}
		if c.country != "" {
			request["country"] = c.country
		}
		r := pd.Do(http.MethodPut, nationalIDPath(p.Id), request)
		if c.want != http.StatusOK {
			refused(t, r, c.want, c.code)
			if leaks(string(r.Body), number) {
				t.Errorf("%s: the refusal carries the number: %s", c.name, r.Body)
			}
			if n := h.Count(t, `SELECT count(*) FROM staff.people WHERE id = $1 AND national_id_mac IS NOT NULL`, p.Id); n != 0 {
				t.Errorf("%s: the number was stored", c.name)
			}
			continue
		}
		if r.Status != http.StatusOK {
			t.Errorf("%s: %d %s, want 200", c.name, r.Status, r.Body)
		}
	}
}

// The sealed fields are staff:personal-data's alone (plan reading 8): HR
// holding staff:manage, the person themselves and a colleague are the
// uniform 403 on every one of the four operations.
func TestSealed_PersonalDataOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	self, user := signIn(t, h)
	link(t, h, p.Id, user)
	colleague, _ := signIn(t, h, "staff:view")
	for name, c := range map[string]*modtest.Client{"staff:manage": hr, "self": self, "staff:view": colleague} {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": testNationalID("018190", 347)}},
			{http.MethodPost, nationalIDRevealPath(p.Id), nil},
			{http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "bban", "value": "12345678903"}},
			{http.MethodPost, bankAccountRevealPath(p.Id), nil},
		} {
			if r := c.Do(req.method, req.path, req.body); r.Status != http.StatusForbidden {
				t.Errorf("%s %s %s: %d, want 403", name, req.method, req.path, r.Status)
			}
		}
	}
}

// The directory's search reads the names, the employee number and the work
// email; the national identity number is sealed, so no part of it can match.
func TestPeople_QNeverMatchesTheNationalID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, _ := signIn(t, h, "staff:personal-data")
	number := testNationalID("018190", 347)
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	do(t, pd, http.MethodPut, nationalIDPath(p.Id), map[string]any{"kind": "fodselsnummer", "value": number}, http.StatusOK, nil)
	for _, q := range []string{number, number[6:], number[:6]} {
		if got := listPeople(t, hr, "?q="+q); len(got.Data) != 0 {
			t.Errorf("q=%s lists %d people, want none", q, len(got.Data))
		}
	}
	if got := listPeople(t, hr, "?q=Kari"); len(got.Data) != 1 {
		t.Errorf("q=Kari lists %d, want the person", len(got.Data))
	}
}

// sentMail is what the bank-change mail fake saw: the message and the last
// four digits the database held when it was handed over.
type sentMail struct {
	out   mail.Outbound
	last4 string
}

// A bank-account change mails the person (design S4, S12): to their private
// email when set, else their work email, in their preferred language,
// carrying the last four digits and nothing more of the account. The mail is
// sent after the change has committed — the fake reads the stored last four
// digits from another connection and finds the new ones — and outside any
// transaction, as the recorder shows. The history carries the kind and the
// last four digits.
func TestBankAccount_ChangeMailsThePerson(t *testing.T) {
	t.Parallel()
	var (
		h    *harness
		mu   sync.Mutex
		sent []sentMail
	)
	mails := func() []sentMail {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sent)
	}
	h = newHarness(t, modtest.WithSMTPSend(func(ctx context.Context, _ config.MailConfig, out mail.Outbound) error {
		var last4 string
		if err := h.Pool().QueryRow(ctx, `SELECT coalesce(max(bank_account_last4), '') FROM staff.people WHERE $1 = ANY(ARRAY[private_email, work_email])`, out.To[0]).Scan(&last4); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMail{out: out, last4: last4})
		return nil
	}))
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signIn(t, h, "staff:personal-data")

	for i, c := range []struct {
		name, language, privateEmail, workEmail string
		kind, value, last4                      string
		to, phrase                              string
	}{
		{"English, private email", "en", "kari@privat.no", "kari@example.no", "bban", "1234.56.78903", "8903", "kari@privat.no", "bank account"},
		{"Norwegian, work email only", "nb", "", "ola@example.no", "iban", "NO93 8601 1117 947", "7947", "ola@example.no", "kontonummer"},
	} {
		body := with(newPerson("Kari", fmt.Sprintf("Nordmann%d", i)), "preferredLanguage", c.language, "workEmail", c.workEmail)
		if c.privateEmail != "" {
			body = with(body, "privateEmail", c.privateEmail)
		}
		p := createPerson(t, hr, body)
		before := len(mails())
		var out gen.StaffPerson
		do(t, pd, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": c.kind, "value": c.value}, http.StatusOK, &out)
		if out.BankAccount == nil || string(out.BankAccount.Kind) != c.kind || out.BankAccount.Last4 != c.last4 {
			t.Errorf("%s: bankAccount = %+v, want %s ending %s", c.name, out.BankAccount, c.kind, c.last4)
		}
		got := mails()
		if len(got) != before+1 {
			t.Fatalf("%s: %d mails, want one", c.name, len(got)-before)
		}
		m := got[len(got)-1]
		if !slices.Equal(m.out.To, []string{c.to}) {
			t.Errorf("%s: to %v, want %s", c.name, m.out.To, c.to)
		}
		if m.last4 != c.last4 {
			t.Errorf("%s: at the send the database held %q, want the committed %s", c.name, m.last4, c.last4)
		}
		text := m.out.Subject + "\n" + m.out.TextBody + "\n" + m.out.HTMLBody
		if !strings.Contains(strings.ToLower(text), c.phrase) {
			t.Errorf("%s: the mail is not in %s: %s", c.name, c.language, text)
		}
		if !strings.Contains(text, c.last4) {
			t.Errorf("%s: the mail does not name the last four digits: %s", c.name, text)
		}
		normalised := strings.NewReplacer(" ", "", ".", "").Replace(c.value)
		if carriesMoreThanLast4(text, normalised) {
			t.Errorf("%s: the mail carries more than the last four digits: %s", c.name, text)
		}
		events := listEvents(t, hr, p.Id)
		set := fieldsOf(events[0])["bankAccount"].(map[string]any)["to"].(map[string]any)
		if events[0].Kind != "bank_account_set" || set["kind"] != c.kind || set["last4"] != c.last4 {
			t.Errorf("%s: latest event = %s %v, want bank_account_set with the kind and last four", c.name, events[0].Kind, set)
		}
	}
	var sends int
	for _, call := range contractCalls.by(pdID) {
		if call.method == "SMTPSend" {
			sends++
			if call.inTx {
				t.Error("the mail was sent inside a transaction")
			}
		}
	}
	if sends != 2 {
		t.Errorf("the recorder saw %d sends, want 2", sends)
	}
}

// The mail is skipped, with a log line and without failing the change, when
// the person has no email at all, and when the installation's mail driver is
// not SMTP.
func TestBankAccount_MailSkipped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		opts  []modtest.Option
		email bool
	}{
		{"no address", nil, false},
		{"the log driver", []modtest.Option{modtest.WithEnv("MAIL_DRIVER", "log")}, true},
	} {
		h := newHarness(t, c.opts...)
		hr, _ := signIn(t, h, "staff:manage")
		pd, _ := signIn(t, h, "staff:personal-data")
		body := newPerson("Kari", "Nordmann")
		if c.email {
			body = with(body, "workEmail", "kari@example.no")
		}
		p := createPerson(t, hr, body)
		do(t, pd, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "bban", "value": "12345678903"}, http.StatusOK, nil)
		if got := h.mail.messages(); len(got) != 0 {
			t.Errorf("%s: %d mails, want none", c.name, len(got))
		}
		if !strings.Contains(h.Logs(), "bank-account change mail skipped") {
			t.Errorf("%s: no log line says the mail was skipped:\n%s", c.name, h.Logs())
		}
	}
}

// The bank account is checked before it is sealed — a refusal never echoes
// it — and revealed once to staff:personal-data with Cache-Control no-store,
// each reveal an event naming the viewer. A person with none on file has
// nothing to reveal.
func TestBankAccount_RevealAndRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	pd, pdID := signIn(t, h, "staff:personal-data")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	if r := pd.Do(http.MethodPost, bankAccountRevealPath(p.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("reveal with none on file: %d, want 404", r.Status)
	}
	for _, c := range []struct{ kind, value string }{
		{"bban", "12345678904"},
		{"iban", "NO9386011117948"},
		{"iban", "DE89370400440532013001"},
	} {
		r := pd.Do(http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": c.kind, "value": c.value})
		refused(t, r, http.StatusBadRequest, "bank_account_invalid")
		if carriesMoreThanLast4(string(r.Body), c.value) {
			t.Errorf("%s %s: the refusal carries the account: %s", c.kind, c.value, r.Body)
		}
	}
	do(t, pd, http.MethodPut, bankAccountPath(p.Id), map[string]any{"kind": "iban", "value": "de89 3704 0044 0532 0130 00"}, http.StatusOK, nil)
	sealed := modtest.One[[]byte](t, h.Harness, `SELECT bank_account_sealed FROM staff.people WHERE id = $1`, p.Id)
	if strings.Contains(string(sealed), "0532013000") {
		t.Error("bank_account_sealed holds the account in clear")
	}
	r := pd.Do(http.MethodPost, bankAccountRevealPath(p.Id), nil)
	if r.Status != http.StatusOK || r.Header("Cache-Control") != "no-store" {
		t.Fatalf("reveal: %d Cache-Control %q, want 200 no-store", r.Status, r.Header("Cache-Control"))
	}
	var value gen.StaffBankAccountReveal
	r.JSON(&value)
	if value.Kind != gen.Iban || value.Value != "DE89370400440532013000" {
		t.Errorf("revealed = %+v, want the IBAN normalised", value)
	}
	events := listEvents(t, hr, p.Id)
	if events[0].Kind != "bank_account_revealed" || events[0].Actor.UserId == nil || *events[0].Actor.UserId != pdID {
		t.Errorf("latest event = %s by %+v, want bank_account_revealed by the viewer", events[0].Kind, events[0].Actor)
	}
	if strings.Contains(h.Logs(), "0532013000") {
		t.Error("a log line carries the account")
	}
}

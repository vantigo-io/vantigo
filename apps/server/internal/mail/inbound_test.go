package mail_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

var update = flag.Bool("update", false, "rewrite testdata/mime/*.golden.json from the parser's answers")

// goldenInbound is what a corpus message's *.golden.json records: the
// Inbound with each attachment's content as its size and SHA-256.
type goldenInbound struct {
	Error              string             `json:"error,omitempty"`
	MessageID          string             `json:"messageId"`
	InReplyTo          string             `json:"inReplyTo"`
	References         []string           `json:"references"`
	From               *mail.Address      `json:"from"`
	To                 []mail.Address     `json:"to"`
	Cc                 []mail.Address     `json:"cc"`
	Subject            string             `json:"subject"`
	Date               string             `json:"date"`
	AutoSubmitted      bool               `json:"autoSubmitted"`
	Text               string             `json:"text"`
	HTML               string             `json:"html"`
	HTMLDropped        bool               `json:"htmlDropped"`
	Attachments        []goldenAttachment `json:"attachments"`
	DroppedAttachments int                `json:"droppedAttachments"`
	SkippedParts       int                `json:"skippedParts"`
}

type goldenAttachment struct {
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	ContentID   string `json:"contentId"`
	Inline      bool   `json:"inline"`
	Size        int    `json:"size"`
	SHA256      string `json:"sha256"`
}

func toGolden(in mail.Inbound, err error) goldenInbound {
	g := goldenInbound{
		MessageID: in.MessageID, InReplyTo: in.InReplyTo, References: in.References,
		From: in.From, To: in.To, Cc: in.Cc, Subject: in.Subject, AutoSubmitted: in.AutoSubmitted,
		Text: in.Text, HTML: in.HTML, HTMLDropped: in.HTMLDropped,
		DroppedAttachments: in.DroppedAttachments, SkippedParts: in.SkippedParts,
	}
	if err != nil {
		g.Error = err.Error()
	}
	if !in.Date.IsZero() {
		g.Date = in.Date.UTC().Format(time.RFC3339)
	}
	for _, a := range in.Attachments {
		sum := sha256.Sum256(a.Content)
		g.Attachments = append(g.Attachments, goldenAttachment{
			FileName: a.FileName, ContentType: a.ContentType, ContentID: a.ContentID,
			Inline: a.Inline, Size: len(a.Content), SHA256: hex.EncodeToString(sum[:]),
		})
	}
	return g
}

func marshalGolden(t *testing.T, g goldenInbound) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func parseCorpus(t *testing.T, name string) (mail.Inbound, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mime", name))
	if err != nil {
		t.Fatal(err)
	}
	return mail.ParseInbound(raw, mail.InboundLimits{})
}

// TestParseInbound_Corpus parses every testdata/mime/*.eml and compares the
// answer with its *.golden.json twin; -update rewrites the twins (format
// them with biome afterwards: bunx biome format --write).
func TestParseInbound_Corpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "mime", "*.eml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			in, err := parseCorpus(t, name)
			got := marshalGolden(t, toGolden(in, err))
			golden := strings.TrimSuffix(file, ".eml") + ".golden.json"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			// Compared as values, not bytes: biome reformats the twins.
			var want goldenInbound
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("%s: %v", golden, err)
			}
			if wantBytes := marshalGolden(t, want); !bytes.Equal(got, wantBytes) {
				t.Errorf("%s differs from its golden twin:\n got: %s\nwant: %s", name, got, wantBytes)
			}
		})
	}
}

// The rules the golden files hold, stated on their own so a wrong -update
// cannot quietly bless a regression.

func TestParseInbound_Plain(t *testing.T) {
	in := mustParse(t, "plain.eml")
	if in.From == nil || in.From.Address != "kari.nordmann@example.no" || in.From.Name != "Kari Nordmann" {
		t.Errorf("From = %+v, want the lower-cased address and the name", in.From)
	}
	if in.MessageID != "<plain-1@mail.example.no>" || in.Subject != "Spørsmål om faktura" {
		t.Errorf("MessageID %q, Subject %q", in.MessageID, in.Subject)
	}
	if !strings.Contains(in.Text, "fakturaen på nytt") || in.HTML != "" || len(in.Attachments) != 0 {
		t.Errorf("Text %q, HTML %q, %d attachments", in.Text, in.HTML, len(in.Attachments))
	}
	if want := time.Date(2026, 10, 27, 8, 15, 0, 0, time.UTC); !in.Date.Equal(want) {
		t.Errorf("Date = %v, want %v", in.Date, want)
	}
}

func TestParseInbound_AlternativeBoth(t *testing.T) {
	in := mustParse(t, "alternative.eml")
	if in.Text != "Hello in plain text, with a soft line break and æ." {
		t.Errorf("Text = %q (quoted-printable)", in.Text)
	}
	if in.HTML != "<p>Hello in <b>HTML</b>, æ.</p>" {
		t.Errorf("HTML = %q (base64)", in.HTML)
	}
	if len(in.To) != 2 || in.To[1].Name != "Second, Person" || len(in.Cc) != 1 || in.Cc[0].Address != "copy@example.com" {
		t.Errorf("To %+v, Cc %+v", in.To, in.Cc)
	}
}

func TestParseInbound_RelatedKeepsTheCIDPartInline(t *testing.T) {
	in := mustParse(t, "related-cid.eml")
	if in.Text != "See the logo." || !strings.Contains(in.HTML, `cid:logo@example.com`) {
		t.Errorf("Text %q, HTML %q", in.Text, in.HTML)
	}
	if len(in.Attachments) != 1 {
		t.Fatalf("%d attachments, want the PNG", len(in.Attachments))
	}
	a := in.Attachments[0]
	if a.ContentID != "logo@example.com" || !a.Inline || a.ContentType != "image/png" || a.FileName != "logo.png" {
		t.Errorf("attachment %+v", a)
	}
	if !bytes.HasPrefix(a.Content, []byte("\x89PNG")) {
		t.Errorf("content is not the PNG (base64 without padding): % x", a.Content[:min(8, len(a.Content))])
	}
}

func TestParseInbound_RFC2047(t *testing.T) {
	in := mustParse(t, "rfc2047-subject.eml")
	if in.Subject != "Faktura for mai – æøå og prøve" {
		t.Errorf("Subject = %q", in.Subject)
	}
	if in.From == nil || in.From.Name != "Per Østbro" {
		t.Errorf("From = %+v", in.From)
	}
}

func TestParseInbound_RFC2231FileNamesInAnyCharset(t *testing.T) {
	in := mustParse(t, "rfc2231-filename.eml")
	var names []string
	for _, a := range in.Attachments {
		names = append(names, a.FileName)
	}
	want := []string{"ærlig faktura.pdf", "prøve åæ.txt", "lång filø.bin", "møte.ics"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("file names = %q, want %q (the Latin-1 one is what mime.ParseMediaType drops)", names, want)
	}
	if in.Text != "Three attachments." {
		t.Errorf("Text = %q: an attachment of type text/plain is not a body", in.Text)
	}
}

func TestParseInbound_Charsets(t *testing.T) {
	latin := mustParse(t, "latin1.eml")
	if !strings.Contains(latin.Text, "prøve på æøå og ÆØÅ") || latin.From.Name != "Bjørn" {
		t.Errorf("latin1: Text %q, From %+v", latin.Text, latin.From)
	}
	cp := mustParse(t, "windows-1252.eml")
	if !strings.Contains(cp.Text, "“Quoted” – it costs 100 € — isn’t it?") {
		t.Errorf("windows-1252: Text %q", cp.Text)
	}
}

func TestParseInbound_WinmailKeptUndecoded(t *testing.T) {
	in := mustParse(t, "winmail.eml")
	if len(in.Attachments) != 2 {
		t.Fatalf("%d attachments, want both TNEF parts", len(in.Attachments))
	}
	for _, a := range in.Attachments {
		if a.FileName != "winmail.dat" || a.ContentType != "application/ms-tnef" || !bytes.HasPrefix(a.Content, []byte{0x78, 0x9f, 0x3e, 0x22}) {
			t.Errorf("attachment %q %q % x", a.FileName, a.ContentType, a.Content[:4])
		}
	}
}

func TestParseInbound_ForwardedMessageKeptWhole(t *testing.T) {
	in := mustParse(t, "forwarded-rfc822.eml")
	if in.Text != "Forwarding this one." {
		t.Errorf("Text = %q: the forwarded body is not this message's", in.Text)
	}
	if len(in.Attachments) != 1 || in.Attachments[0].ContentType != "message/rfc822" || in.Attachments[0].FileName != "message.eml" ||
		!bytes.Contains(in.Attachments[0].Content, []byte("Message-ID: <orig-1@example.com>")) {
		t.Errorf("attachments %+v", in.Attachments)
	}
}

func TestParseInbound_AutoReplyHeaders(t *testing.T) {
	for name, want := range map[string]bool{
		"auto-reply.eml": true, "auto-reply-suppress.eml": true, "auto-reply-precedence.eml": true,
		"auto-reply-xautoreply.eml": true, "auto-submitted-no.eml": false, "plain.eml": false,
	} {
		if got := mustParse(t, name).AutoSubmitted; got != want {
			t.Errorf("%s: AutoSubmitted = %v, want %v", name, got, want)
		}
	}
}

func TestParseInbound_References(t *testing.T) {
	in := mustParse(t, "references.eml")
	if len(in.References) != 25 || in.References[0] != "<ref-01@example.com>" || in.References[24] != "<ref-25@example.com>" {
		t.Errorf("References = %q, want the 25 folded ids oldest first", in.References)
	}
	if in.InReplyTo != "<ref-25@example.com>" {
		t.Errorf("InReplyTo = %q, want the first id only", in.InReplyTo)
	}
}

func TestParseInbound_ReferencesCapKeepsFirstAndNewest(t *testing.T) {
	var ids []string
	for i := range 150 {
		ids = append(ids, fmt.Sprintf("<r%03d@example.com>", i))
	}
	raw := "From: a@example.com\nReferences: " + strings.Join(ids, "\n ") + "\n\nbody\n"
	in, err := mail.ParseInbound([]byte(raw), mail.InboundLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.References) != 100 || in.References[0] != ids[0] || in.References[1] != ids[51] || in.References[99] != ids[149] {
		t.Errorf("References kept %d: first %q, second %q, last %q", len(in.References), in.References[0], in.References[1], in.References[99])
	}
}

func TestParseInbound_Unreadable(t *testing.T) {
	for _, name := range []string{"malformed.eml", "broken-boundary.eml"} {
		_, err := parseCorpus(t, name)
		if !errors.Is(err, mail.ErrUnparseable) {
			t.Errorf("%s: err = %v, want ErrUnparseable", name, err)
		}
	}
	// An error never carries the message's own text.
	_, err := parseCorpus(t, "malformed.eml")
	if err != nil && strings.Contains(err.Error(), "colon") {
		t.Errorf("error %q quotes the message", err)
	}
	in, err := parseCorpus(t, "broken-boundary.eml")
	if in.Subject != "A boundary that never appears" {
		t.Errorf("the headers that parsed are kept beside the error: %q (%v)", in.Subject, err)
	}
}

func TestParseInbound_NoFromWithABodyReads(t *testing.T) {
	in := mustParse(t, "no-from.eml")
	if in.From != nil || in.Text != "A body without a sender." {
		t.Errorf("From %+v, Text %q", in.From, in.Text)
	}
}

func TestParseInbound_DepthLimit(t *testing.T) {
	in := mustParse(t, "deep.eml")
	if in.Text != "Shallow text." || in.SkippedParts == 0 {
		t.Errorf("Text %q, SkippedParts %d: the part 12 levels down is beyond MaxDepth 10", in.Text, in.SkippedParts)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "mime", "deep.eml"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := mail.ParseInbound(raw, mail.InboundLimits{MaxDepth: 20})
	if err != nil || !strings.Contains(all.Text, "Too deep to be read.") {
		t.Errorf("with MaxDepth 20: Text %q, %v", all.Text, err)
	}
}

func TestParseInbound_PartAndAttachmentLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: a@example.com\nContent-Type: multipart/mixed; boundary=B\n\n")
	for i := range 8 {
		fmt.Fprintf(&b, "--B\nContent-Type: application/octet-stream\nContent-Disposition: attachment; filename=f%d.bin\n\nx\n", i)
	}
	b.WriteString("--B--\n")
	in, err := mail.ParseInbound([]byte(b.String()), mail.InboundLimits{MaxAttachments: 3})
	if err != nil || len(in.Attachments) != 3 || in.DroppedAttachments != 5 {
		t.Errorf("%d attachments, %d dropped, %v", len(in.Attachments), in.DroppedAttachments, err)
	}
	in, err = mail.ParseInbound([]byte(b.String()), mail.InboundLimits{MaxParts: 4})
	if err != nil || len(in.Attachments) != 3 || in.SkippedParts == 0 {
		t.Errorf("MaxParts 4 (the root and three): %d attachments, %d skipped, %v", len(in.Attachments), in.SkippedParts, err)
	}
}

func TestParseInbound_BodyCaps(t *testing.T) {
	raw := "From: a@example.com\nContent-Type: multipart/alternative; boundary=B\n\n" +
		"--B\nContent-Type: text/plain; charset=utf-8\n\n" + strings.Repeat("æ", 100) + "\n" +
		"--B\nContent-Type: text/html\n\n<p>" + strings.Repeat("x", 300) + "</p>\n--B--\n"
	in, err := mail.ParseInbound([]byte(raw), mail.InboundLimits{MaxBody: 101})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Text) > 101 || !strings.HasSuffix(in.Text, "…") || !strings.HasPrefix(in.Text, "ææ") {
		t.Errorf("Text (%d bytes) = %q, want cut with … on a rune boundary", len(in.Text), in.Text)
	}
	if in.HTML != "" || !in.HTMLDropped {
		t.Errorf("HTML %q, HTMLDropped %v: an HTML body over the cap is dropped", in.HTML, in.HTMLDropped)
	}
}

func TestParseInbound_OutlookShape(t *testing.T) {
	in := mustParse(t, "outlook.eml")
	if in.InReplyTo != "<0123456789abcdef0123456789abcdef@vantigo.invalid>" || len(in.References) != 1 {
		t.Errorf("InReplyTo %q, References %q", in.InReplyTo, in.References)
	}
	if !strings.Contains(in.Text, "Vi går videre.") || !strings.Contains(in.HTML, "Vi går videre.") {
		t.Errorf("Text %q, HTML %q", in.Text, in.HTML)
	}
	if len(in.Cc) != 1 || in.Cc[0].Address != "regnskap@kunde.example.no" {
		t.Errorf("Cc = %+v", in.Cc)
	}
}

func TestParseInbound_OwnMessage(t *testing.T) {
	in := mustParse(t, "own-message.eml")
	if in.MessageID != "<0123456789abcdef0123456789abcdef@vantigo.invalid>" {
		t.Errorf("MessageID = %q", in.MessageID)
	}
}

func TestNormalizeMessageID(t *testing.T) {
	for in, want := range map[string]string{
		"<Abc@Example.com>":     "<Abc@Example.com>",
		"  <abc@example.com>  ": "<abc@example.com>",
		"abc@example.com":       "<abc@example.com>",
		"<a@x> <b@x>":           "<a@x>",
		"":                      "",
		"<>":                    "",
		"<a b@x>":               "",
		"<a\x00b@x>":            "",
		"<a\x01b@x>":            "",
		"<a\x7fb@x>":            "",
	} {
		if got := mail.NormalizeMessageID(in); got != want {
			t.Errorf("NormalizeMessageID(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustParse(t *testing.T, name string) mail.Inbound {
	t.Helper()
	in, err := parseCorpus(t, name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return in
}

// TestParseInbound_StripsNUL: no string ParseInbound answers holds a NUL —
// Postgres refuses one in text and jsonb, and a message that always fails
// its insert would hold its channel — and an id with a control character in
// it is no id.
func TestParseInbound_StripsNUL(t *testing.T) {
	in := mustParse(t, "nul.eml")
	if in.Subject != "Hei der" || in.SkippedParts != 1 || in.From == nil || in.From.Name != "Kari Nordmann" {
		t.Errorf("Subject %q, SkippedParts %d, From %+v", in.Subject, in.SkippedParts, in.From)
	}
	if in.MessageID != "" || in.InReplyTo != "<ok-1@example.no>" || len(in.References) != 1 || in.References[0] != "<ok-1@example.no>" {
		t.Errorf("MessageID %q, InReplyTo %q, References %q: an id with a NUL is none", in.MessageID, in.InReplyTo, in.References)
	}
	if in.Text != "A body with a NUL." || !strings.Contains(in.HTML, "HTML too") {
		t.Errorf("Text %q, HTML %q", in.Text, in.HTML)
	}
	if len(in.Attachments) != 1 || in.Attachments[0].FileName != "f.bin" || in.Attachments[0].ContentID != "cid@example.no" {
		t.Errorf("Attachments = %+v", in.Attachments)
	}
	for _, s := range []string{in.Subject, in.Text, in.HTML, in.From.Name, in.Attachments[0].FileName, in.Attachments[0].ContentID} {
		if strings.ContainsRune(s, 0) {
			t.Errorf("%q holds a NUL", s)
		}
	}
}

// TestParseInbound_DropsAnAddressTooLong: an address longer than 320
// characters (RFC 5321's 64 + 1 + 255) is no address; 320 is one.
func TestParseInbound_DropsAnAddressTooLong(t *testing.T) {
	in := mustParse(t, "long-from.eml")
	if in.From != nil {
		t.Errorf("From = %+v, want none: 321 characters", in.From)
	}
	if len(in.To) != 2 || len(in.To[1].Address) != 320 {
		t.Errorf("To = %d addresses, want both, the 320-character one kept", len(in.To))
	}
}

// TestParseInbound_CapsAddressesAndIDs: To and Cc keep at most 100 addresses
// each, and an id header is read for at most 400 ids.
func TestParseInbound_CapsAddressesAndIDs(t *testing.T) {
	var cc, refs []string
	for i := range 5000 {
		cc = append(cc, fmt.Sprintf("p%04d@example.com", i))
		refs = append(refs, fmt.Sprintf("<r%04d@example.com>", i))
	}
	raw := "From: a@example.com\nTo: " + strings.Join(cc, ", ") + "\nCc: " + strings.Join(cc, ", ") +
		"\nReferences: " + strings.Join(refs, " ") + "\n\nbody\n"
	in, err := mail.ParseInbound([]byte(raw), mail.InboundLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.To) != 100 || len(in.Cc) != 100 || in.Cc[0].Address != "p0000@example.com" || in.Cc[99].Address != "p0099@example.com" {
		t.Errorf("To %d, Cc %d addresses, want the first 100 of each", len(in.To), len(in.Cc))
	}
	if len(in.References) != 100 || in.References[99] != "<r0399@example.com>" {
		t.Errorf("References %d, last %q: want the newest of the first 400 ids", len(in.References), in.References[len(in.References)-1])
	}
}

// TestParseInbound_NoContentTypeIsPlainText: a message with no MIME headers
// is text/plain (RFC 2045 §5.2), its body the text.
func TestParseInbound_NoContentTypeIsPlainText(t *testing.T) {
	in := mustParse(t, "no-content-type.eml")
	if in.Text != "A plain RFC 5322 message\nwithout MIME headers." || len(in.Attachments) != 0 {
		t.Errorf("Text %q, %d attachments", in.Text, len(in.Attachments))
	}
}

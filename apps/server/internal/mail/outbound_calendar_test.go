package mail_test

import (
	"bufio"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
	"testing"

	gomail "net/mail"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// calendarBody is a minimal iCalendar object: the part's content is opaque to
// the mail package, which carries it as given.
const calendarBody = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Vantigo//Calendar//EN\r\nMETHOD:REQUEST\r\n" +
	"BEGIN:VEVENT\r\nUID:abc@vantigo.invalid\r\nSUMMARY:Kundemøte\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// mimePart is one leaf or container of a parsed message: its media type, its
// parameters, its disposition and, for a leaf, its decoded content.
type mimePart struct {
	mediaType   string
	params      map[string]string
	disposition string
	dispParams  map[string]string
	children    []mimePart
	content     string
}

// parseMIME reads the whole of data, as the SMTP server received it, into a
// tree of parts, decoding each leaf's transfer encoding.
func parseMIME(t *testing.T, data string) mimePart {
	t.Helper()
	msg, err := gomail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("reading the message: %v", err)
	}
	return parseEntity(t, textproto.MIMEHeader(msg.Header), msg.Body)
}

func parseEntity(t *testing.T, header textproto.MIMEHeader, body io.Reader) mimePart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parsing Content-Type %q: %v", header.Get("Content-Type"), err)
	}
	p := mimePart{mediaType: mediaType, params: params}
	if d := header.Get("Content-Disposition"); d != "" {
		if p.disposition, p.dispParams, err = mime.ParseMediaType(d); err != nil {
			t.Fatalf("parsing Content-Disposition %q: %v", d, err)
		}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		r := multipart.NewReader(body, params["boundary"])
		for {
			part, err := r.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("reading a %s part: %v", mediaType, err)
			}
			p.children = append(p.children, parseEntity(t, part.Header, part))
		}
		return p
	}
	raw, err := io.ReadAll(bufio.NewReader(body))
	if err != nil {
		t.Fatalf("reading a %s body: %v", mediaType, err)
	}
	p.content = decodeTransfer(t, header.Get("Content-Transfer-Encoding"), string(raw))
	return p
}

// TestSendOutbound_CalendarPartIsTheThirdAlternativeAndTheAttachment proves
// the iMIP layout the clients key on (calendar invitations design D3):
// multipart/mixed holding a multipart/alternative of text, HTML and
// text/calendar with method=, then the same body attached as invite.ics.
func TestSendOutbound_CalendarPartIsTheThirdAlternativeAndTheAttachment(t *testing.T) {
	data, _ := sendOutboundThroughTestServer(t, mail.Outbound{
		DisplayName: "Kari Nordmann",
		To:          []string{"ola@example.test"},
		Subject:     "Invitation: Kundemøte",
		TextBody:    "plain body here",
		HTMLBody:    "<p>html body here</p>",
		Calendar:    &mail.CalendarPart{Method: "REQUEST", Body: calendarBody},
	})

	if !strings.Contains(data, "Content-Type: text/calendar; method=REQUEST; charset=UTF-8") {
		t.Errorf("no text/calendar part with method=REQUEST and charset=UTF-8; DATA:\n%s", data)
	}
	root := parseMIME(t, data)
	if root.mediaType != "multipart/mixed" || len(root.children) != 2 {
		t.Fatalf("root = %s with %d parts, want multipart/mixed with the alternative and the attachment; DATA:\n%s", root.mediaType, len(root.children), data)
	}
	alt := root.children[0]
	var types []string
	for _, c := range alt.children {
		types = append(types, c.mediaType)
	}
	if alt.mediaType != "multipart/alternative" || strings.Join(types, ",") != "text/plain,text/html,text/calendar" {
		t.Fatalf("first part = %s of %v, want multipart/alternative of text/plain, text/html, text/calendar", alt.mediaType, types)
	}
	cal := alt.children[2]
	if cal.params["method"] != "REQUEST" || !strings.EqualFold(cal.params["charset"], "UTF-8") {
		t.Errorf("text/calendar parameters = %v, want method=REQUEST and charset=UTF-8", cal.params)
	}
	// The test server keeps each line without its CRLF and joins them with
	// LF, so the quoted-printable alternative's own line ends arrive as LF;
	// the base64 attachment below carries the CRLFs byte for byte.
	if want := strings.ReplaceAll(calendarBody, "\r\n", "\n"); cal.content != want {
		t.Errorf("text/calendar content = %q, want the body as given", cal.content)
	}
	att := root.children[1]
	if att.mediaType != "application/ics" || att.disposition != "attachment" || att.dispParams["filename"] != "invite.ics" {
		t.Errorf("second part = %s, %s %v, want application/ics attached as invite.ics", att.mediaType, att.disposition, att.dispParams)
	}
	if att.content != calendarBody {
		t.Errorf("invite.ics content = %q, want the body as given", att.content)
	}
}

// TestSendOutbound_CalendarCancelIsAttachedAsCancelICS proves a cancellation
// carries method=CANCEL and names its attachment cancel.ics.
func TestSendOutbound_CalendarCancelIsAttachedAsCancelICS(t *testing.T) {
	body := strings.Replace(calendarBody, "METHOD:REQUEST", "METHOD:CANCEL", 1)
	data, _ := sendOutboundThroughTestServer(t, mail.Outbound{
		To:       []string{"ola@example.test"},
		Subject:  "Cancelled: Kundemøte",
		TextBody: "plain body here",
		HTMLBody: "<p>html body here</p>",
		Calendar: &mail.CalendarPart{Method: "CANCEL", Body: body},
	})

	root := parseMIME(t, data)
	if len(root.children) != 2 || len(root.children[0].children) != 3 {
		t.Fatalf("parts = %+v, want the alternative of three and the attachment", root)
	}
	if cal := root.children[0].children[2]; cal.mediaType != "text/calendar" || cal.params["method"] != "CANCEL" {
		t.Errorf("third alternative = %s %v, want text/calendar with method=CANCEL", cal.mediaType, cal.params)
	}
	if att := root.children[1]; att.dispParams["filename"] != "cancel.ics" || att.content != body {
		t.Errorf("attachment = %v with %q, want cancel.ics carrying the body", att.dispParams, att.content)
	}
}

// TestSendOutbound_CalendarRefusesAnotherMethod proves the part carries only
// the two methods the calendar sends: anything else is the caller's mistake,
// refused before a connection is made.
func TestSendOutbound_CalendarRefusesAnotherMethod(t *testing.T) {
	for _, method := range []string{"", "REPLY", "request"} {
		_, err := mail.RenderOutboundForTests("noreply@example.test", mail.Outbound{
			To: []string{"ola@example.test"}, TextBody: "x",
			Calendar: &mail.CalendarPart{Method: method, Body: calendarBody},
		})
		if err == nil {
			t.Errorf("method %q: rendered, want an error", method)
		}
	}
}

// decodeTransfer undoes a leaf's Content-Transfer-Encoding.
func decodeTransfer(t *testing.T, encoding, raw string) string {
	t.Helper()
	switch strings.ToLower(encoding) {
	case "base64":
		out, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
		if err != nil {
			t.Fatalf("decoding base64: %v", err)
		}
		return string(out)
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(raw)))
		if err != nil {
			t.Fatalf("decoding quoted-printable: %v", err)
		}
		return string(out)
	default:
		return raw
	}
}

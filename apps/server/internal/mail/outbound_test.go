package mail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// These tests drive SendOutbound — and through it Outbound.message()'s
// rendering — against the same in-process SMTP server smtp_test.go already
// uses for Sender and VerifyConnection, rather than against a substituted
// seam. Fix round 1's Important 1: every communications test replaces
// Deps.SMTPSend with a fake, so without these the whole rendering path
// (Message-ID bracketing, References joining, the text+HTML alternative,
// embed-vs-attach) shipped unexercised, and every module that later sends mail
// would inherit it untested.

// sendOutboundThroughTestServer sends out through a fresh in-process SMTP
// server over STARTTLS with AUTH, and returns the exact DATA the server
// received — headers and body as they went on the wire.
func sendOutboundThroughTestServer(t *testing.T, out mail.Outbound) (data, rcptTo string) {
	t.Helper()
	cert, pool := testTLSMaterial(t)
	restore := mail.AllowLoopbackForTests(pool)
	defer restore()

	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) {
		s.offerSTARTTLS = true
		s.offerAUTH = true
	})
	host, port := hostPort(t, srv.addr())

	err := mail.SendOutbound(context.Background(), config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test",
		Username: "smtp-user", Password: "smtp-pass", TLS: "starttls",
	}, false, out)
	if err != nil {
		t.Fatalf("SendOutbound() = %v, want nil", err)
	}
	srv.waitDone(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.data, srv.rcptTo
}

// TestSendOutbound_RendersTheFullEnvelope is the one test that exercises every
// rendering rule at once, because they interact: an alternative body plus an
// embed plus an attachment is also the MIME nesting most likely to break.
func TestSendOutbound_RendersTheFullEnvelope(t *testing.T) {
	data, rcptTo := sendOutboundThroughTestServer(t, mail.Outbound{
		DisplayName: "Vantigo Support",
		To:          []string{"to@example.test"},
		Cc:          []string{"cc@example.test"},
		Bcc:         []string{"bcc@example.test"},
		Subject:     "Your enquiry",
		TextBody:    "plain body here",
		HTMLBody:    `<p>html body here</p>`,
		MessageID:   "0123456789abcdef0123456789abcdef@vantigo.invalid",
		InReplyTo:   "<parent@example.test>",
		References:  []string{"<first@example.test>", "<second@example.test>"},
		Attachments: []mail.Attachment{
			{FileName: "quote.txt", ContentType: "text/plain", Content: []byte("attached bytes")},
			{FileName: "logo.png", ContentType: "image/png", Content: []byte("png bytes"),
				ContentID: "logo-cid", Inline: true},
		},
	})

	// The From display name, and the address from the configuration.
	if !strings.Contains(data, `"Vantigo Support" <noreply@example.test>`) {
		t.Errorf("From header missing the display name form; DATA:\n%s", data)
	}
	// Recipients: To and Cc are headers, and every class is an SMTP recipient.
	// Bcc is delivered by envelope only, so it must be the last RCPT TO and
	// must NOT appear as a header.
	for _, want := range []string{"To: <to@example.test>", "Cc: <cc@example.test>"} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %q in DATA:\n%s", want, data)
		}
	}
	if strings.Contains(data, "Bcc: bcc@example.test") {
		t.Errorf("Bcc leaked into the headers, want it envelope-only; DATA:\n%s", data)
	}
	if !strings.Contains(rcptTo, "bcc@example.test") {
		t.Errorf("last RCPT TO = %q, want the bcc recipient to have been delivered to", rcptTo)
	}

	// Rule 1: MessageID is stored BARE and rendered bracketed exactly once.
	// Passing an already-bracketed value would emit "<<…>>", which is the
	// bug this asserts against.
	if !strings.Contains(data, "Message-ID: <0123456789abcdef0123456789abcdef@vantigo.invalid>") {
		t.Errorf("Message-ID not rendered in the single-bracketed form; DATA:\n%s", data)
	}
	if strings.Contains(data, "<<") {
		t.Errorf("Message-ID double-bracketed; DATA:\n%s", data)
	}

	// Rule 2: References are joined with WHITESPACE per RFC 5322, never
	// comma-joined the way an address-style header would be.
	if !strings.Contains(data, "References: <first@example.test> <second@example.test>") {
		t.Errorf("References not whitespace-joined; DATA:\n%s", data)
	}
	if strings.Contains(data, "<first@example.test>,") {
		t.Errorf("References comma-joined, want whitespace-joined; DATA:\n%s", data)
	}
	if !strings.Contains(data, "In-Reply-To: <parent@example.test>") {
		t.Errorf("In-Reply-To missing; DATA:\n%s", data)
	}

	// Rule 3: text + HTML becomes a multipart/alternative carrying both.
	if !strings.Contains(data, "multipart/alternative") {
		t.Errorf("no multipart/alternative for a text+HTML message; DATA:\n%s", data)
	}
	for _, want := range []string{"text/plain", "text/html", "plain body here"} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %q in DATA:\n%s", want, data)
		}
	}

	// Rule 4: an inline part is EMBEDDED with its Content-ID (so an HTML
	// cid: reference resolves), while a normal file is ATTACHED.
	if !strings.Contains(data, "quote.txt") || !strings.Contains(data, "logo.png") {
		t.Errorf("attachment filenames missing; DATA:\n%s", data)
	}
	// The Content-ID must be ANGLE-BRACKETED: RFC 2392 cid: resolution matches
	// an HTML body's `src="cid:logo-cid"` against the bracketed header value,
	// and go-mail writes whatever it is given verbatim — unlike MimeKit, whose
	// ContentId setter brackets it for you. Handing it a bare value therefore
	// ships an inline image that no client can resolve. Found by this test.
	if !strings.Contains(data, "Content-Id: <logo-cid>") {
		t.Errorf("inline part's Content-Id is not angle-bracketed; DATA:\n%s", data)
	}
	if !strings.Contains(data, "attachment;") {
		t.Errorf("no attachment disposition for the non-inline file; DATA:\n%s", data)
	}
	if !strings.Contains(data, "inline;") {
		t.Errorf("no inline disposition for the embedded file; DATA:\n%s", data)
	}
}

// TestSendOutbound_TextOnlyIsNotMultipart proves the alternative is only built
// when there really are two bodies: a text-only message stays a simple
// text/plain, which is what every message this port actually sends looks like.
func TestSendOutbound_TextOnlyIsNotMultipart(t *testing.T) {
	data, _ := sendOutboundThroughTestServer(t, mail.Outbound{
		To: []string{"to@example.test"}, Subject: "Plain", TextBody: "just text",
	})
	if strings.Contains(data, "multipart/alternative") {
		t.Errorf("text-only message rendered as multipart/alternative; DATA:\n%s", data)
	}
	if !strings.Contains(data, "text/plain") || !strings.Contains(data, "just text") {
		t.Errorf("text body missing; DATA:\n%s", data)
	}
}

// TestSendOutbound_HTMLOnlyIsSentAsHTML proves an HTML-only message is sent as
// text/html rather than silently as an empty text/plain.
func TestSendOutbound_HTMLOnlyIsSentAsHTML(t *testing.T) {
	data, _ := sendOutboundThroughTestServer(t, mail.Outbound{
		To: []string{"to@example.test"}, Subject: "Rich", HTMLBody: "<p>only html</p>",
	})
	if !strings.Contains(data, "text/html") {
		t.Errorf("HTML-only message not sent as text/html; DATA:\n%s", data)
	}
	if strings.Contains(data, "multipart/alternative") {
		t.Errorf("HTML-only message rendered as multipart/alternative; DATA:\n%s", data)
	}
}

// TestSendOutbound_OmitsMessageIDHeaderWhenUnset proves the Message-ID is the
// caller's to choose: no MessageID means go-mail's own generated one is not
// forced into a caller-chosen shape by us.
func TestSendOutbound_OmitsCallerMessageIDWhenUnset(t *testing.T) {
	data, _ := sendOutboundThroughTestServer(t, mail.Outbound{
		To: []string{"to@example.test"}, Subject: "No id", TextBody: "body",
	})
	if strings.Contains(data, "@vantigo.invalid") {
		t.Errorf("a Message-ID was invented; DATA:\n%s", data)
	}
}

// TestSendOutbound_RequiresARecipient proves the recipient check happens
// before any connection is attempted: a message with no To, Cc or Bcc is
// refused rather than dialled out and rejected by the server.
func TestSendOutbound_RequiresARecipient(t *testing.T) {
	err := mail.SendOutbound(context.Background(), config.MailConfig{
		Driver: "smtp", Host: "127.0.0.1", Port: 1, From: "noreply@example.test", TLS: "starttls",
	}, false, mail.Outbound{Subject: "nobody", TextBody: "body"})
	if err == nil {
		t.Fatal("SendOutbound() = nil error, want a refusal for a message with no recipient")
	}
}

// TestSendOutbound_GuardRejectsLoopbackByDefault proves SendOutbound dials
// through the same DNS-rebinding guard Sender and VerifyConnection use — the
// property that makes this a safe shared port rather than a second, weaker
// send path.
func TestSendOutbound_GuardRejectsLoopbackByDefault(t *testing.T) {
	cert, _ := testTLSMaterial(t)
	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) { s.offerSTARTTLS = true })
	host, port := hostPort(t, srv.addr())

	err := mail.SendOutbound(context.Background(), config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test", TLS: "starttls",
	}, false, mail.Outbound{To: []string{"to@example.test"}, Subject: "hi", TextBody: "hi"})
	if err == nil {
		t.Fatal("SendOutbound() = nil error, want the guard to reject a loopback destination")
	}
}

// TestSendOutbound_RejectsPlaintextWithoutInsecureTransport proves SendOutbound
// inherits the driver's fail-closed construction rather than bypassing it.
func TestSendOutbound_RejectsPlaintextWithoutInsecureTransport(t *testing.T) {
	err := mail.SendOutbound(context.Background(), config.MailConfig{
		Driver: "smtp", Host: "127.0.0.1", Port: 25, From: "noreply@example.test", TLS: "none",
	}, false, mail.Outbound{To: []string{"to@example.test"}, Subject: "hi", TextBody: "hi"})
	if err == nil {
		t.Fatal("SendOutbound() = nil error, want TLS=none refused without insecure transport")
	}
}

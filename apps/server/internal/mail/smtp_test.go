package mail_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// testTLSMaterial mints a certificate the same way net/http/httptest does
// (a well-known self-signed cert covering 127.0.0.1, ::1 and example.com)
// and hands back both the server-side tls.Certificate and a pool that
// trusts it, so the SMTP test server and the driver under test can share
// one certificate without generating one by hand.
func testTLSMaterial(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	defer ts.Close()

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return ts.TLS.Certificates[0], pool
}

// smtpTestServer is a minimal in-process SMTP server: just enough of the
// protocol (greeting, EHLO with STARTTLS and AUTH PLAIN advertised when
// configured, the STARTTLS upgrade, MAIL/RCPT/DATA/QUIT) to drive the smtp
// driver end to end and record what it sent.
type smtpTestServer struct {
	ln net.Listener

	offerSTARTTLS bool
	offerAUTH     bool
	tlsConfig     *tls.Config // used for the STARTTLS upgrade
	implicitTLS   bool        // the listener itself is wrapped in TLS

	done chan struct{}

	mu       sync.Mutex
	mailFrom string
	rcptTo   string
	data     string
	sawTLS   bool
	authUser string
	authPass string
}

func newSMTPTestServer(t *testing.T, cert tls.Certificate, opts func(*smtpTestServer)) *smtpTestServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	srv := &smtpTestServer{
		ln:        ln,
		tlsConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		done:      make(chan struct{}),
	}
	if opts != nil {
		opts(srv)
	}
	if srv.implicitTLS {
		srv.ln = tls.NewListener(ln, srv.tlsConfig)
	}

	go srv.serveOne()
	t.Cleanup(func() { _ = ln.Close() })
	return srv
}

func (s *smtpTestServer) addr() string { return s.ln.Addr().String() }

// waitDone blocks until the one connection this server handles has run to
// completion (the client sent QUIT, or the connection dropped), so the test
// can safely read the recorded fields afterward.
func (s *smtpTestServer) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("smtp test server did not finish handling the connection")
	}
}

func (s *smtpTestServer) serveOne() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	writeLine(conn, "220 mail.example.test ESMTP ready")

	inData := false
	var dataBuf strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.data = dataBuf.String()
				s.mu.Unlock()
				writeLine(conn, "250 2.0.0 OK: queued")
				continue
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "."))
			dataBuf.WriteString("\n")
			continue
		}

		verb, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			lines := []string{"250-mail.example.test greets you"}
			if s.offerSTARTTLS {
				lines = append(lines, "250-STARTTLS")
			}
			if s.offerAUTH {
				lines = append(lines, "250-AUTH PLAIN")
			}
			lines = append(lines, "250 8BITMIME")
			for _, l := range lines {
				writeLine(conn, l)
			}
		case "STARTTLS":
			writeLine(conn, "220 2.0.0 Ready to start TLS")
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
			s.mu.Lock()
			s.sawTLS = true
			s.mu.Unlock()
		case "AUTH":
			mech, payload, _ := strings.Cut(rest, " ")
			if strings.EqualFold(mech, "PLAIN") && payload != "" {
				decoded, err := base64.StdEncoding.DecodeString(payload)
				if err == nil {
					parts := strings.Split(string(decoded), "\x00")
					if len(parts) == 3 {
						s.mu.Lock()
						s.authUser, s.authPass = parts[1], parts[2]
						s.mu.Unlock()
					}
				}
			}
			writeLine(conn, "235 2.7.0 Authentication successful")
		case "MAIL":
			s.mu.Lock()
			s.mailFrom = rest
			s.mu.Unlock()
			writeLine(conn, "250 2.1.0 OK")
		case "RCPT":
			s.mu.Lock()
			s.rcptTo = rest
			s.mu.Unlock()
			writeLine(conn, "250 2.1.5 OK")
		case "DATA":
			inData = true
			writeLine(conn, "354 Start mail input; end with <CRLF>.<CRLF>")
		case "QUIT":
			writeLine(conn, "221 2.0.0 Bye")
			return
		case "NOOP", "RSET":
			writeLine(conn, "250 2.0.0 OK")
		default:
			writeLine(conn, "500 5.5.1 unrecognized command")
		}
	}
}

func writeLine(w net.Conn, s string) {
	_, _ = w.Write([]byte(s + "\r\n"))
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("net.SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing port %q: %v", portStr, err)
	}
	return host, port
}

// TestSMTP_RejectsPlaintextWithoutInsecureTransport proves the driver fails
// closed on construction: TLS="none" is refused unless the caller says
// insecure transport is allowed, defensively re-checking what config
// validation already guarantees.
func TestSMTP_RejectsPlaintextWithoutInsecureTransport(t *testing.T) {
	_, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: "127.0.0.1", Port: 25, From: "noreply@example.test", TLS: "none",
	}, false)
	if err == nil {
		t.Fatal("NewSMTP() = nil error, want an error for TLS=none without insecure transport")
	}
}

// TestSMTP_GuardRejectsLoopbackByDefault proves the DNS-rebinding guard is
// wired into the real driver, not only unit-tested in isolation: without the
// test-only loopback hook, sending to a loopback host is rejected before
// any connection is attempted.
func TestSMTP_GuardRejectsLoopbackByDefault(t *testing.T) {
	cert, _ := testTLSMaterial(t)
	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) { s.offerSTARTTLS = true })
	host, port := hostPort(t, srv.addr())

	sender, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test", TLS: "starttls",
	}, false)
	if err != nil {
		t.Fatalf("NewSMTP() = %v, want nil", err)
	}

	err = sender.Send(context.Background(), mail.Message{To: "person@example.test", Subject: "hi", TextBody: "hi"})
	if err == nil {
		t.Fatal("Send() = nil error, want the guard to reject a loopback destination")
	}
}

// TestSMTP_STARTTLSIsRequired proves TLS=starttls fails closed rather than
// silently sending in plaintext when the server does not offer STARTTLS.
func TestSMTP_STARTTLSIsRequired(t *testing.T) {
	restore := mail.AllowLoopbackForTests(nil)
	defer restore()

	cert, _ := testTLSMaterial(t)
	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) { s.offerSTARTTLS = false })
	host, port := hostPort(t, srv.addr())

	sender, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test", TLS: "starttls",
	}, false)
	if err != nil {
		t.Fatalf("NewSMTP() = %v, want nil", err)
	}

	err = sender.Send(context.Background(), mail.Message{To: "person@example.test", Subject: "hi", TextBody: "hi"})
	if err == nil {
		t.Fatal("Send() = nil error, want an error when the server does not support STARTTLS")
	}
}

// TestSMTP_SendsOverSTARTTLS proves a successful send negotiates STARTTLS,
// authenticates, and delivers a message with the From, To, Subject and a
// text/plain body.
func TestSMTP_SendsOverSTARTTLS(t *testing.T) {
	cert, pool := testTLSMaterial(t)
	restore := mail.AllowLoopbackForTests(pool)
	defer restore()

	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) {
		s.offerSTARTTLS = true
		s.offerAUTH = true
	})
	host, port := hostPort(t, srv.addr())

	sender, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test",
		Username: "smtp-user", Password: "smtp-pass", TLS: "starttls",
	}, false)
	if err != nil {
		t.Fatalf("NewSMTP() = %v, want nil", err)
	}

	err = sender.Send(context.Background(), mail.Message{
		To: "person@example.test", Subject: "Reset your password", TextBody: "Use the link.",
	})
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	srv.waitDone(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.sawTLS {
		t.Fatal("the server never saw a STARTTLS upgrade")
	}
	if !strings.Contains(srv.mailFrom, "noreply@example.test") {
		t.Fatalf("MAIL FROM = %q, want it to contain the From address", srv.mailFrom)
	}
	if !strings.Contains(srv.rcptTo, "person@example.test") {
		t.Fatalf("RCPT TO = %q, want it to contain the recipient", srv.rcptTo)
	}
	if !strings.Contains(srv.data, "From:") || !strings.Contains(srv.data, "noreply@example.test") {
		t.Fatalf("message data %q lacks a From header", srv.data)
	}
	if !strings.Contains(srv.data, "To:") || !strings.Contains(srv.data, "person@example.test") {
		t.Fatalf("message data %q lacks a To header", srv.data)
	}
	if !strings.Contains(srv.data, "Subject: Reset your password") {
		t.Fatalf("message data %q lacks the Subject header", srv.data)
	}
	if !strings.Contains(srv.data, "text/plain") {
		t.Fatalf("message data %q is not text/plain", srv.data)
	}
	if !strings.Contains(srv.data, "Use the link.") {
		t.Fatalf("message data %q lacks the text body", srv.data)
	}
	if srv.authUser != "smtp-user" || srv.authPass != "smtp-pass" {
		t.Fatalf("AUTH PLAIN credentials = %q/%q, want smtp-user/smtp-pass", srv.authUser, srv.authPass)
	}
}

// TestSMTP_SendsOverImplicitTLS proves TLS="implicit" negotiates TLS
// immediately on connect, before any SMTP command is sent in plaintext.
func TestSMTP_SendsOverImplicitTLS(t *testing.T) {
	cert, pool := testTLSMaterial(t)
	restore := mail.AllowLoopbackForTests(pool)
	defer restore()

	srv := newSMTPTestServer(t, cert, func(s *smtpTestServer) { s.implicitTLS = true })
	host, port := hostPort(t, srv.addr())

	sender, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test", TLS: "implicit",
	}, false)
	if err != nil {
		t.Fatalf("NewSMTP() = %v, want nil", err)
	}

	err = sender.Send(context.Background(), mail.Message{
		To: "person@example.test", Subject: "hi", TextBody: "hi there",
	})
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	srv.waitDone(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.Contains(srv.rcptTo, "person@example.test") {
		t.Fatalf("RCPT TO = %q, want it to contain the recipient", srv.rcptTo)
	}
}

// TestSMTP_SendsPlaintextWhenInsecureIsAllowed proves TLS="none" with
// insecure transport allowed sends over an unencrypted connection.
func TestSMTP_SendsPlaintextWhenInsecureIsAllowed(t *testing.T) {
	restore := mail.AllowLoopbackForTests(nil)
	defer restore()

	cert, _ := testTLSMaterial(t)
	srv := newSMTPTestServer(t, cert, nil)
	host, port := hostPort(t, srv.addr())

	sender, err := mail.NewSMTP(config.MailConfig{
		Driver: "smtp", Host: host, Port: port, From: "noreply@example.test", TLS: "none",
	}, true)
	if err != nil {
		t.Fatalf("NewSMTP() = %v, want nil", err)
	}

	err = sender.Send(context.Background(), mail.Message{
		To: "person@example.test", Subject: "hi", TextBody: "hi there",
	})
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	srv.waitDone(t)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.sawTLS {
		t.Fatal("the server saw a TLS upgrade, want a plaintext exchange")
	}
	if !strings.Contains(srv.rcptTo, "person@example.test") {
		t.Fatalf("RCPT TO = %q, want it to contain the recipient", srv.rcptTo)
	}
}

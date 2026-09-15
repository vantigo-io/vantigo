package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	gomail "github.com/wneessen/go-mail"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// testRootCAs, when non-nil, replaces the system trust pool for the SMTP
// driver's own tests, so they can trust an in-process test server's
// self-signed certificate. Production code has no configuration field that
// reaches it — see export_test.go, compiled only by `go test`.
var testRootCAs *x509.CertPool

// smtpSender delivers mail through an SMTP server using go-mail
// (github.com/wneessen/go-mail). Its DialContextFunc — guardedDialContext —
// runs the DNS-rebinding guard immediately before opening the socket, and
// connects to the exact address that passed the check rather than
// re-resolving the hostname, closing the window a rebinding attack would
// otherwise open between the check and the connect.
type smtpSender struct {
	client *gomail.Client
	from   string
}

// NewSMTP builds a Sender that delivers through cfg's SMTP server, in
// whichever TLS mode cfg names: "implicit", "starttls" (mandatory, never
// opportunistic) or "none" — the operator's choice per deployment.
func NewSMTP(cfg config.MailConfig) (Sender, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("mail: smtp host is required")
	}
	if cfg.From == "" {
		return nil, fmt.Errorf("mail: smtp from address is required")
	}

	guard := newDestinationGuard(&net.Resolver{})

	tlsConfig := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: gomail.DefaultTLSMinVersion,
		RootCAs:    testRootCAs,
	}

	opts := []gomail.Option{gomail.WithPort(cfg.Port)}

	switch cfg.TLS {
	case "implicit":
		// go-mail only wraps the connection in TLS itself when the default
		// dialer is used; a custom DialContextFunc — which the guard needs —
		// bypasses that wrapping, so guardedDialContext performs the TLS
		// handshake for implicit TLS. TLSPolicy NoTLS stops go-mail from
		// also trying STARTTLS on top of the already-encrypted connection.
		opts = append(opts,
			gomail.WithTLSPolicy(gomail.NoTLS),
			gomail.WithDialContextFunc(guardedDialContext(guard, tlsConfig)),
		)
	case "starttls":
		opts = append(opts,
			gomail.WithTLSPolicy(gomail.TLSMandatory),
			gomail.WithTLSConfig(tlsConfig),
			gomail.WithDialContextFunc(guardedDialContext(guard, nil)),
		)
	case "none":
		opts = append(opts,
			gomail.WithTLSPolicy(gomail.NoTLS),
			gomail.WithDialContextFunc(guardedDialContext(guard, nil)),
		)
	default:
		return nil, fmt.Errorf("mail: unknown smtp TLS mode %q", cfg.TLS)
	}

	if cfg.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(cfg.Username),
			gomail.WithPassword(cfg.Password),
		)
	}

	client, err := gomail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: building smtp client: %w", err)
	}

	return &smtpSender{client: client, from: cfg.From}, nil
}

func (s *smtpSender) Send(ctx context.Context, m Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("mail: invalid from address: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("mail: invalid recipient address: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.TextBody)

	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: sending: %w", err)
	}
	return nil
}

// VerifyConnection builds a client over cfg exactly as NewSMTP does — the
// same guarded, DNS-rebinding-safe dial path, the same TLS mode and auth
// handling — but only connects, authenticates
// if a username is configured, and disconnects: no message is ever built or
// sent. Communications channel verification
// (POST .../channels/{id}/verify, communications inventory §15.2's
// "VerifyAsync runs the same ExecuteAsync path... and then immediately
// disconnects") is this function's one caller; it exists so that endpoint
// reuses the exact guard NewSMTP's Sender dials through rather than
// re-implementing SMTP connection handling for a verify-only path.
func VerifyConnection(ctx context.Context, cfg config.MailConfig) error {
	s, err := newSMTPClient(cfg)
	if err != nil {
		return err
	}
	if err := s.client.DialWithContext(ctx); err != nil {
		return err
	}
	return s.client.Close()
}

// newSMTPClient builds a client over cfg through NewSMTP — the same TLS
// modes and guarded dial the Sender uses — and returns it as the concrete
// type, so the two callers that need more than Sender's Send
// (VerifyConnection, which dials without sending, and SendOutbound, which
// renders a full envelope) share one construction and one type assertion
// rather than repeating the downcast.
func newSMTPClient(cfg config.MailConfig) (*smtpSender, error) {
	sender, err := NewSMTP(cfg)
	if err != nil {
		return nil, err
	}
	s, ok := sender.(*smtpSender)
	if !ok {
		return nil, fmt.Errorf("mail: unexpected sender implementation %T", sender)
	}
	return s, nil
}

// guardedDialContext returns a DialContextFunc that resolves the SMTP
// host, checks the resolved address with guard, and connects directly to
// that vetted address — never re-resolving the hostname — so a DNS answer
// that changes between the check and the connect cannot smuggle a
// disallowed destination past the guard.
//
// When tlsConfig is non-nil, it also performs the TLS handshake itself:
// go-mail's own implicit-TLS wrapping only runs for its default dialer, not
// for a custom DialContextFunc like this one.
func guardedDialContext(guard *destinationGuard, tlsConfig *tls.Config) gomail.DialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("mail: invalid smtp address %q: %w", address, err)
		}
		addr, err := guard.Check(ctx, host)
		if err != nil {
			return nil, err
		}
		dialAddr := net.JoinHostPort(addr.String(), port)

		if tlsConfig != nil {
			dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig}
			return dialer.DialContext(ctx, network, dialAddr)
		}
		dialer := &net.Dialer{}
		return dialer.DialContext(ctx, network, dialAddr)
	}
}

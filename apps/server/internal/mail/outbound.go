package mail

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	gomail "github.com/wneessen/go-mail"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// Attachment is one file carried by an Outbound: the bytes, the filename and
// content type the recipient sees, and — for an inline image referenced from
// an HTML body by cid: — its Content-ID.
//
// Content is bytes rather than an io.Reader because the one producer
// (communications' outbox worker) has already read the object out of the
// object store to check it exists before the send, and because a send that
// fails has to be retried from the same value: a consumed Reader could not be.
type Attachment struct {
	FileName    string
	ContentType string
	Content     []byte
	ContentID   string
	Inline      bool
}

// Outbound is one full outbound message, as opposed to Message's three-field
// application mail: several recipient classes, both body types, a caller-chosen
// Message-ID, threading headers and attachments.
//
// It exists for communications, whose outbox worker sends a conversation
// message through a *per-channel* SMTP configuration rather than the process's
// own configured Sender — .NET's EmailEnvelope
// (SV/Models/EmailEnvelope.cs, communications inventory §15.1) reduced to what
// this port actually populates. Identity's invitation and reset mails keep
// using Message and Sender; nothing here changes that path.
//
// MessageID is the *bare* form — "0123…@vantigo.invalid", no angle brackets —
// because go-mail's SetMessageIDWithValue adds them. Passing an already
// bracketed value would emit "<<…>>".
type Outbound struct {
	// DisplayName is the From display name; empty sends a bare address.
	DisplayName string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	TextBody    string
	HTMLBody    string
	MessageID   string
	InReplyTo   string
	References  []string
	Attachments []Attachment
}

// SendOutbound delivers out through the SMTP server cfg names, building the
// client exactly as NewSMTP does — the same TLS modes, the same guarded,
// DNS-rebinding-safe dial, the same auth handling — and connecting
// only for this one message.
//
// It takes a config.MailConfig per call rather than being a method on a
// long-lived Sender because its caller (communications' outbox worker) resolves
// the server from the *channel's* stored credentials, which differ per channel
// and can change between sends; cfg.From is the channel's own address. This is
// the same shape VerifyConnection already has, for the same reason.
//
// The caller bounds the send with ctx. Communications derives that deadline
// from its lease so a send can never outlive the lease that protects it
// (inventory §15.2's `0 < timeout < lease` invariant).
func SendOutbound(ctx context.Context, cfg config.MailConfig, out Outbound) error {
	if len(out.To)+len(out.Cc)+len(out.Bcc) == 0 {
		return fmt.Errorf("mail: an outbound message needs at least one recipient")
	}
	s, err := newSMTPClient(cfg)
	if err != nil {
		return err
	}
	msg, err := out.message(s.from)
	if err != nil {
		return err
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("mail: sending: %w", err)
	}
	return nil
}

// bracketedContentID returns id in the angle-bracketed form a Content-ID
// header takes, adding the brackets only when the caller did not.
//
// go-mail writes the value verbatim, where MimeKit's ContentId setter (which
// .NET's SmtpDeliveryProvider relies on, inventory §15.5) brackets it. An
// unbracketed Content-ID does not match an HTML body's `src="cid:…"` under
// RFC 2392, so an inline image would arrive unresolvable — which is why this
// normalisation lives here rather than at every call site.
func bracketedContentID(id string) string {
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		return id
	}
	return "<" + id + ">"
}

// message renders out as a go-mail message sent from the address from.
func (o Outbound) message(from string) (*gomail.Msg, error) {
	msg := gomail.NewMsg()
	if o.DisplayName != "" {
		if err := msg.FromFormat(o.DisplayName, from); err != nil {
			return nil, fmt.Errorf("mail: invalid from address: %w", err)
		}
	} else if err := msg.From(from); err != nil {
		return nil, fmt.Errorf("mail: invalid from address: %w", err)
	}
	if len(o.To) > 0 {
		if err := msg.To(o.To...); err != nil {
			return nil, fmt.Errorf("mail: invalid recipient address: %w", err)
		}
	}
	if len(o.Cc) > 0 {
		if err := msg.Cc(o.Cc...); err != nil {
			return nil, fmt.Errorf("mail: invalid cc address: %w", err)
		}
	}
	if len(o.Bcc) > 0 {
		if err := msg.Bcc(o.Bcc...); err != nil {
			return nil, fmt.Errorf("mail: invalid bcc address: %w", err)
		}
	}
	msg.Subject(o.Subject)
	if o.MessageID != "" {
		msg.SetMessageIDWithValue(o.MessageID)
	}
	if o.InReplyTo != "" {
		msg.SetGenHeader(gomail.HeaderInReplyTo, o.InReplyTo)
	}
	if len(o.References) > 0 {
		// RFC 5322 separates References with whitespace, not commas, so the
		// list is joined here rather than handed to SetGenHeader as separate
		// values (which it would comma-join as an address-style list).
		msg.SetGenHeader(gomail.HeaderReferences, strings.Join(o.References, " "))
	}

	switch {
	case o.TextBody != "" && o.HTMLBody != "":
		msg.SetBodyString(gomail.TypeTextPlain, o.TextBody)
		msg.AddAlternativeString(gomail.TypeTextHTML, o.HTMLBody)
	case o.HTMLBody != "":
		msg.SetBodyString(gomail.TypeTextHTML, o.HTMLBody)
	default:
		msg.SetBodyString(gomail.TypeTextPlain, o.TextBody)
	}

	for _, a := range o.Attachments {
		opts := []gomail.FileOption{}
		if a.ContentType != "" {
			opts = append(opts, gomail.WithFileContentType(gomail.ContentType(a.ContentType)))
		}
		// An inline part is embedded with its Content-ID so an HTML body's
		// cid: reference resolves; everything else is a plain attachment.
		if a.Inline && a.ContentID != "" {
			opts = append(opts, gomail.WithFileContentID(bracketedContentID(a.ContentID)))
			if err := msg.EmbedReader(a.FileName, bytes.NewReader(a.Content), opts...); err != nil {
				return nil, fmt.Errorf("mail: embedding %q: %w", a.FileName, err)
			}
			continue
		}
		if err := msg.AttachReader(a.FileName, bytes.NewReader(a.Content), opts...); err != nil {
			return nil, fmt.Errorf("mail: attaching %q: %w", a.FileName, err)
		}
	}
	return msg, nil
}

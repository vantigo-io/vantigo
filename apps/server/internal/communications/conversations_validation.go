package communications

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
)

// This file is the Conversations area's value validators (ValidateConversation
// / ValidateNote / ValidateBody / ValidateRecipients / AddDuplicateRecipientError,
// EP/Dtos/CommunicationValidation.cs, communications inventory §3.1's field
// table). It reuses channels_validation.go's utf16Length and this module's
// own validChannelAddress (CommunicationValidation.IsEmail is the exact same
// rule for both channel addresses and recipient emails — one validator, two
// call sites, same as .NET's single IsEmail).

// containsControl reports whether v contains any Unicode control character
// (char.IsControl's rough Go equivalent, the same check validDisplayName in
// channels_validation.go already performs for a different field).
func containsControl(v string) bool {
	for _, r := range v {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// normalizeEmail is EmailSuppression.Normalize (SV/EmailSuppression.cs):
// Trim().ToUpperInvariant(). Ported exactly as-is (design doc D7) — this is
// a different normalisation than the contact linker's lowercase form used
// elsewhere in the .NET module, and the asymmetry is deliberate, not a bug
// to fix.
func normalizeEmail(v string) string { return strings.ToUpper(strings.TrimSpace(v)) }

// preview is ConversationEndpoints.Preview (`:443`): nil for a blank value,
// else the first 500 UTF-16 code units — .NET's `value.Length <= 500 ?
// value : value[..500]`, reproduced with utf16.Encode/Decode rather than a
// Go byte or rune truncation so the cut point matches .NET's exactly.
func preview(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	units := utf16.Encode([]rune(v))
	if len(units) <= 500 {
		return &v
	}
	truncated := string(utf16.Decode(units[:500]))
	return &truncated
}

// validateSubjectAndBody is ValidateBody (EP/Dtos/CommunicationValidation.cs:80-90),
// shared by ValidateConversation (subjectRequired=true) and .NET's
// ValidateReply (subjectRequired=false, not this task's scope but kept
// general so a later task can reuse it rather than re-deriving the same
// rule). Two independent subject checks can both fire in sequence — a blank
// subject when required sets the "required" message, then, because
// TrimSpace(subject) is still "" for a blank value, the second check never
// overwrites it; a non-blank-but-invalid subject only ever hits the second
// check. This mirrors .NET's two `if` statements exactly, including which
// one wins when both could apply.
func validateSubjectAndBody(subject, text, html *string, subjectRequired bool) map[string][]string {
	errs := map[string][]string{}
	s := ""
	if subject != nil {
		s = *subject
	}
	invalidSubject := utf16Length(s) > 998 || s != strings.TrimSpace(s) || containsControl(s)
	if subjectRequired && (strings.TrimSpace(s) == "" || invalidSubject) {
		errs["subject"] = []string{"Subject is required, must be at most 998 characters, and cannot contain surrounding whitespace or control characters."}
	}
	if strings.TrimSpace(s) != "" && invalidSubject {
		errs["subject"] = []string{"Subject is invalid."}
	}

	t, h := "", ""
	if text != nil {
		t = *text
	}
	if html != nil {
		h = *html
	}
	if strings.TrimSpace(t) == "" && strings.TrimSpace(h) == "" {
		errs["body"] = []string{"TextBody or HtmlBody is required."}
	}
	// len(t)/len(h) is already a UTF-8 byte count: a Go string decoded from
	// JSON is UTF-8-encoded, so this needs no Encoding.UTF8.GetByteCount
	// equivalent the way utf16Length needs unicode/utf16 for Length.
	if text != nil && len(t) > 1024*1024 {
		errs["textBody"] = []string{"TextBody must be at most 1 MiB."}
	}
	if html != nil && len(h) > 1024*1024 {
		errs["htmlBody"] = []string{"HtmlBody must be at most 1 MiB."}
	}
	return errs
}

// validateEmailRecipients is ValidateRecipients (`:92-98`), for both "to"
// (required = request.To is not null: an explicitly-empty "to" array is
// still an error, an omitted one is not, by itself) and "cc" (required is
// always false).
func validateEmailRecipients(errs map[string][]string, name string, recipients []gen.EmailRecipientRequest, required bool) {
	if required && len(recipients) == 0 {
		errs[name] = []string{"At least one recipient is required."}
		return
	}
	if len(recipients) > 100 {
		errs[name] = []string{"At most 100 recipients are allowed."}
	}
	for i, r := range recipients {
		if r.Email == nil || !validChannelAddress(*r.Email) {
			errs[recipientFieldKey(name, i)] = []string{"A valid email address is required."}
		}
	}
}

func recipientFieldKey(name string, index int) string {
	return name + "[" + strconv.Itoa(index) + "]"
}

// addDuplicateRecipientError is AddDuplicateRecipientError (`:100-104`): to
// and cc are pooled, normalised (EmailSuppression.Normalize — uppercased,
// per D7), and checked for any repeat; "recipients" (never "to" or "cc")
// carries the error, and a blank/nil email is ignored the same way .NET's
// `Where(item => item?.Email is not null)` ignores it.
func addDuplicateRecipientError(errs map[string][]string, to, cc []gen.EmailRecipientRequest) {
	seen := map[string]bool{}
	for _, r := range append(append([]gen.EmailRecipientRequest{}, to...), cc...) {
		if r.Email == nil {
			continue
		}
		n := normalizeEmail(*r.Email)
		if seen[n] {
			errs["recipients"] = []string{"A recipient may appear only once."}
			return
		}
		seen[n] = true
	}
}

// validateConversation is ValidateConversation (`:11-26`): the create-
// conversation body's field errors, collected together rather than
// short-circuited on the first failure — the same "report every failure
// together" style this codebase's other validators use.
func validateConversation(body gen.CreateConversationRequest) map[string][]string {
	errs := validateSubjectAndBody(body.Subject, body.TextBody, body.HtmlBody, true)

	var recipients []gen.ChannelRecipientRequest
	if body.Recipients != nil {
		recipients = *body.Recipients
	}
	toProvided := body.To != nil
	var to []gen.EmailRecipientRequest
	if body.To != nil {
		to = *body.To
	}
	var cc []gen.EmailRecipientRequest
	if body.Cc != nil {
		cc = *body.Cc
	}

	if len(recipients) == 0 && len(to) == 0 {
		errs["to"] = []string{"At least one recipient is required."}
	}
	if len(recipients) > 100 {
		errs["recipients"] = []string{"At most 100 recipients are allowed."}
	}
	for i, r := range recipients {
		address := ""
		if r.Address != nil {
			address = *r.Address
		}
		if r.ParticipantId == nil && !validChannelAddress(address) {
			errs[recipientFieldKey("recipients", i)] = []string{"A participant id or valid channel address is required."}
		}
	}
	validateEmailRecipients(errs, "to", to, toProvided)
	validateEmailRecipients(errs, "cc", cc, false)
	addDuplicateRecipientError(errs, to, cc)
	return errs
}

// normalizedReplyMode is `request.ReplyMode?.Trim().ToLowerInvariant() ??
// "reply"` role in both ValidateReply (`:31`) and QueueOutboundAsync
// (`:278`, `var replyMode = request.ReplyMode?.Trim().ToLowerInvariant() ??
// "reply"`) — a single helper so validateReply's acceptance rule and the
// handler's own use of the resolved mode can never drift apart. An omitted
// replyMode defaults to "reply" (the contract's own documented default,
// design doc §4.1; ConversationDtos.cs:17's DTO-level initialiser is what
// makes omission valid at all) and is never rejected.
func normalizedReplyMode(v *string) string {
	if v == nil {
		return "reply"
	}
	return strings.ToLower(strings.TrimSpace(*v))
}

// validateReply is ValidateReply (`:28-37`): unlike ValidateConversation,
// subject is optional (subjectRequired: false — a reply's subject falls
// back to the conversation's own, EP/ConversationEndpoints.cs:282), and two
// fields ValidateConversation never touches are added: replyMode (must
// normalise to "reply" or "reply_all") and attachmentIds (at most 20, no
// duplicate id — the duplicate message overwrites the count message
// exactly as inventory §19.2 item 6 describes for this same field).
func validateReply(body gen.ReplyRequest) map[string][]string {
	errs := validateSubjectAndBody(body.Subject, body.TextBody, body.HtmlBody, false)

	mode := normalizedReplyMode(body.ReplyMode)
	if mode != "reply" && mode != "reply_all" {
		errs["replyMode"] = []string{"ReplyMode must be reply or reply_all."}
	}

	if body.AttachmentIds != nil {
		ids := *body.AttachmentIds
		if len(ids) > 20 {
			errs["attachmentIds"] = []string{"At most 20 attachments are allowed."}
		}
		seen := make(map[string]bool, len(ids))
		duplicate := false
		for _, id := range ids {
			key := id.String()
			if seen[key] {
				duplicate = true
				break
			}
			seen[key] = true
		}
		if duplicate {
			// Overwrites the count message above, matching .NET's second
			// `if` unconditionally replacing whatever `errors["attachmentIds"]`
			// already held (`:34-35`).
			errs["attachmentIds"] = []string{"An attachment may appear only once."}
		}
	}
	return errs
}

// validateNote is ValidateNote (`:39-44`).
func validateNote(body gen.NoteRequest) map[string][]string {
	errs := map[string][]string{}
	if body.TextBody == nil || strings.TrimSpace(*body.TextBody) == "" {
		errs["textBody"] = []string{"TextBody is required."}
	}
	return errs
}

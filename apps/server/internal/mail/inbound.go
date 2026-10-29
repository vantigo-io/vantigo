package mail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

// Inbound is a received message as ParseInbound reads it: the headers
// threading, the linker and the reply need, the bodies, and the attachments.
// Nothing in it is sanitised: HTML is the message's own, for SanitizeHTML.
// No string in it holds a NUL: Postgres refuses one in text and jsonb.
type Inbound struct {
	// MessageID and InReplyTo are bracketed ("<id>"), "" when absent;
	// InReplyTo is the header's first id only. An id with a control
	// character in it is none.
	MessageID, InReplyTo string
	// References are bracketed, oldest first, distinct, at most
	// maxReferences: when a thread holds more, the first and the newest are
	// kept. A header is read for at most maxIDTokens ids.
	References []string
	// From is nil when no From address parses. To and Cc hold at most
	// maxAddresses each; an address longer than maxAddressLength is none.
	From   *Address
	To, Cc []Address
	// Subject is RFC 2047 decoded, trimmed, at most 998 bytes.
	Subject string
	// Date is the Date header, zero when it does not parse.
	Date time.Time
	// AutoSubmitted is set by any of Auto-Submitted (not "no"),
	// X-Auto-Response-Suppress, Precedence bulk/auto_reply/list or
	// X-Autoreply.
	AutoSubmitted bool
	// Text and HTML are the bodies decoded to UTF-8; either may be "". Text
	// has its line breaks as "\n" and no trailing white space; Text above InboundLimits.MaxBody is cut with "…"; HTML above it is dropped
	// and HTMLDropped set.
	Text, HTML  string
	HTMLDropped bool
	Attachments []InboundAttachment
	// DroppedAttachments counts the attachments beyond
	// InboundLimits.MaxAttachments; SkippedParts the parts beyond MaxParts or
	// MaxDepth and the damaged ones.
	DroppedAttachments, SkippedParts int
}

// Address is a mailbox of a From, To or Cc header: Name decoded, Address
// the lower-cased addr-spec.
type Address struct{ Name, Address string }

// InboundAttachment is a part of a received message that is not one of its
// bodies. ContentID is without brackets; Inline is set for a part a body
// can reference by "cid:" (a Content-ID and no attachment disposition).
// FileName is the message's own, unsanitised, "" when it names none.
type InboundAttachment struct {
	FileName, ContentType, ContentID string
	Inline                           bool
	Content                          []byte
}

// InboundLimits bound what ParseInbound walks. A zero field takes the
// default of DefaultInboundLimits.
type InboundLimits struct {
	MaxParts, MaxDepth, MaxAttachments int
	// MaxBody is the cap, in bytes, on each of Text and HTML.
	MaxBody int64
}

// DefaultInboundLimits are the limits a zero InboundLimits field takes.
var DefaultInboundLimits = InboundLimits{MaxParts: 200, MaxDepth: 10, MaxAttachments: 100, MaxBody: 1 << 20}

// ErrUnparseable is the error ParseInbound answers, wrapped with a fixed
// reason, for a message it cannot read: no header block, or neither a From
// nor a body. Its text never holds anything of the message.
var ErrUnparseable = errors.New("mail: the message could not be read")

const (
	maxSubjectBytes = 998
	maxReferences   = 100
	// maxIDTokens is the most ids one Message-Id, In-Reply-To or References
	// value is read for, before References' dedupe and cap.
	maxIDTokens = 4 * maxReferences
	// maxAddresses is the most addresses a To or Cc answers.
	maxAddresses = 100
	// maxAddressLength is RFC 5321's longest path: 64 + 1 + 255.
	maxAddressLength = 320
)

// ParseInbound reads raw, an RFC 5322 message, under limits. A damaged part
// is skipped, not fatal. On ErrUnparseable the Inbound holds whatever
// headers did parse.
func ParseInbound(raw []byte, limits InboundLimits) (Inbound, error) {
	limits = limits.withDefaults()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Inbound{}, fmt.Errorf("%w: no header block", ErrUnparseable)
	}
	header := textproto.MIMEHeader(msg.Header)

	in := Inbound{
		MessageID:     NormalizeMessageID(header.Get("Message-Id")),
		InReplyTo:     firstMessageID(header.Get("In-Reply-To")),
		References:    parseReferences(header.Get("References")),
		Subject:       truncateUTF8(strings.TrimSpace(withoutNUL(decodeHeader(header.Get("Subject")))), maxSubjectBytes),
		AutoSubmitted: autoSubmitted(header),
	}
	if from := parseAddressList(header.Get("From")); len(from) > 0 {
		in.From = &from[0]
	}
	in.To = parseAddressList(header.Get("To"))
	in.Cc = parseAddressList(header.Get("Cc"))
	if date, err := netmail.ParseDate(header.Get("Date")); err == nil {
		in.Date = date
	}

	w := walker{limits: limits, in: &in}
	w.part(header, msg.Body, 0)
	in.Text = strings.TrimRight(strings.ReplaceAll(withoutNUL(strings.Join(w.texts, "\n")), "\r\n", "\n"), " \t\r\n")
	in.HTML = withoutNUL(w.html)
	if int64(len(in.Text)) > limits.MaxBody {
		in.Text = truncateUTF8(in.Text, int(limits.MaxBody)-len("…")) + "…"
	}
	if int64(len(in.HTML)) > limits.MaxBody {
		in.HTML, in.HTMLDropped = "", true
	}

	if in.From == nil && in.Text == "" && in.HTML == "" && len(in.Attachments) == 0 {
		return in, fmt.Errorf("%w: neither a From nor a body", ErrUnparseable)
	}
	return in, nil
}

func (l InboundLimits) withDefaults() InboundLimits {
	d := DefaultInboundLimits
	if l.MaxParts <= 0 {
		l.MaxParts = d.MaxParts
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxAttachments <= 0 {
		l.MaxAttachments = d.MaxAttachments
	}
	if l.MaxBody <= 0 {
		l.MaxBody = d.MaxBody
	}
	return l
}

// NormalizeMessageID answers the first Message-Id in value bracketed and
// trimmed — "<id>" — or "" when there is none. Case is kept: ids are
// compared exactly. It is the key of an inbound receipt, so Communications
// and the fakes agree on it.
func NormalizeMessageID(value string) string {
	ids := messageIDs(value)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func firstMessageID(value string) string { return NormalizeMessageID(value) }

// messageIDs answers each id of a Message-Id, In-Reply-To or References
// value, bracketed: the <…> tokens when there are any, else each
// whitespace-separated word; at most maxIDTokens of them are read.
func messageIDs(value string) []string {
	var ids []string
	rest := value
	for tokens := 0; tokens < maxIDTokens; tokens++ {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			break
		}
		end := strings.IndexByte(rest[open:], '>')
		if end < 0 {
			break
		}
		if id := cleanMessageID(rest[open+1 : open+end]); id != "" {
			ids = append(ids, id)
		}
		rest = rest[open+end+1:]
	}
	if len(ids) > 0 || strings.ContainsAny(value, "<>") {
		return ids
	}
	for i, word := range strings.Fields(value) {
		if i == maxIDTokens {
			break
		}
		if id := cleanMessageID(word); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// cleanMessageID is id bracketed, or "" when it is empty or holds a bracket,
// a space or any control character (NUL among them).
func cleanMessageID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "<> ") || strings.IndexFunc(id, isControl) >= 0 {
		return ""
	}
	return "<" + id + ">"
}

// isControl is a C0 control character or DEL.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// withoutNUL is s with every NUL removed.
func withoutNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

func parseReferences(value string) []string {
	var refs []string
	seen := map[string]bool{}
	for _, id := range messageIDs(value) {
		if !seen[id] {
			seen[id] = true
			refs = append(refs, id)
		}
	}
	if len(refs) > maxReferences {
		refs = append(refs[:1], refs[len(refs)-(maxReferences-1):]...)
	}
	return refs
}

func autoSubmitted(h textproto.MIMEHeader) bool {
	if v, ok := h["Auto-Submitted"]; ok && len(v) > 0 && !strings.EqualFold(strings.TrimSpace(v[0]), "no") {
		return true
	}
	if _, ok := h["X-Auto-Response-Suppress"]; ok {
		return true
	}
	if _, ok := h["X-Autoreply"]; ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "auto_reply", "list":
		return true
	}
	return false
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := lookupCharset(charset)
	if err != nil {
		return nil, err
	}
	return enc.NewDecoder().Reader(input), nil
}

func lookupCharset(charset string) (encoding.Encoding, error) {
	return htmlindex.Get(strings.Trim(strings.TrimSpace(charset), `"`))
}

// decodeHeader decodes RFC 2047 words; a value that does not decode is kept
// as it is, made valid UTF-8.
func decodeHeader(value string) string {
	decoded, err := wordDecoder.DecodeHeader(value)
	if err != nil {
		decoded = value
	}
	return strings.ToValidUTF8(decoded, "�")
}

// parseAddressList answers the addresses of a header that parse: the list
// whole, else each comma-separated piece on its own; NULs removed first, an
// address longer than maxAddressLength or with a control character dropped,
// at most maxAddresses.
func parseAddressList(value string) []Address {
	value = withoutNUL(value)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parser := netmail.AddressParser{WordDecoder: wordDecoder}
	list, err := parser.ParseList(value)
	if err != nil {
		list = nil
		for _, piece := range strings.Split(value, ",") {
			if a, err := parser.Parse(piece); err == nil {
				list = append(list, a)
			}
		}
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		if a.Address == "" || len(a.Address) > maxAddressLength || strings.IndexFunc(a.Address, isControl) >= 0 {
			continue
		}
		out = append(out, Address{Name: strings.TrimSpace(withoutNUL(a.Name)), Address: strings.ToLower(a.Address)})
		if len(out) == maxAddresses {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// walker walks a message's MIME tree, depth first, collecting its bodies
// and attachments.
type walker struct {
	limits  InboundLimits
	in      *Inbound
	parts   int
	texts   []string
	html    string
	hasHTML bool
}

func (w *walker) part(h textproto.MIMEHeader, body io.Reader, depth int) {
	w.parts++
	if w.parts > w.limits.MaxParts {
		w.in.SkippedParts++
		return
	}
	// An absent Content-Type is text/plain (RFC 2045 §5.2): a message
	// without MIME headers keeps its body.
	contentType := h.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "text/plain"
	}
	mediaType, params := parseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		w.multipart(params["boundary"], body, depth)
		return
	}
	content, err := decodeTransfer(h.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		w.in.SkippedParts++
		return
	}
	w.leaf(h, mediaType, params, content)
}

func (w *walker) multipart(boundary string, body io.Reader, depth int) {
	if boundary == "" || depth+1 > w.limits.MaxDepth {
		w.in.SkippedParts++
		return
	}
	reader := multipart.NewReader(body, boundary)
	for {
		p, err := reader.NextRawPart()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				w.in.SkippedParts++
			}
			return
		}
		w.part(p.Header, p, depth+1)
		if w.parts > w.limits.MaxParts {
			return
		}
	}
}

func (w *walker) leaf(h textproto.MIMEHeader, mediaType string, params map[string]string, content []byte) {
	disposition, dispParams := parseMediaType(h.Get("Content-Disposition"))
	fileName := attachmentFileName(h, dispParams, params)
	isAttachment := disposition == "attachment" || fileName != ""

	switch {
	case mediaType == "text/plain" && !isAttachment:
		w.texts = append(w.texts, decodeCharset(content, params["charset"]))
		return
	case mediaType == "text/html" && !isAttachment:
		if !w.hasHTML {
			w.html, w.hasHTML = decodeCharset(content, params["charset"]), true
		}
		return
	case mediaType == "message/rfc822" && fileName == "":
		fileName = "message.eml"
	case mediaType == "application/ms-tnef" && fileName == "":
		fileName = "winmail.dat"
	}

	if len(w.in.Attachments) >= w.limits.MaxAttachments {
		w.in.DroppedAttachments++
		return
	}
	contentID := strings.Trim(strings.TrimSpace(withoutNUL(h.Get("Content-Id"))), "<> ")
	w.in.Attachments = append(w.in.Attachments, InboundAttachment{
		FileName:    withoutNUL(fileName),
		ContentType: mediaType,
		ContentID:   contentID,
		Inline:      contentID != "" && disposition != "attachment",
		Content:     content,
	})
}

// parseMediaType answers value's lower-cased media type and parameters,
// keeping what parsed of a damaged value.
func parseMediaType(value string) (string, map[string]string) {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil && mediaType == "" {
		head, _, _ := strings.Cut(value, ";")
		mediaType = strings.ToLower(strings.TrimSpace(head))
	}
	if params == nil {
		params = map[string]string{}
	}
	return mediaType, params
}

// attachmentFileName answers a part's file name: the Content-Disposition's
// filename, then the Content-Type's name, each as RFC 2231 in any charset
// (mime.ParseMediaType decodes only UTF-8 and US-ASCII and drops the rest),
// else the plain parameter with any RFC 2047 words decoded.
func attachmentFileName(h textproto.MIMEHeader, dispParams, typeParams map[string]string) string {
	if name := rfc2231Param(h.Get("Content-Disposition"), "filename"); name != "" {
		return name
	}
	if name := dispParams["filename"]; name != "" {
		return decodeHeader(name)
	}
	if name := rfc2231Param(h.Get("Content-Type"), "name"); name != "" {
		return name
	}
	if name := typeParams["name"]; name != "" {
		return decodeHeader(name)
	}
	return ""
}

// rfc2231Param reads parameter name of a header value as RFC 2231 —
// name*=charset'lang'value, or its continuations name*0*, name*1… — and
// decodes its charset through htmlindex. "" when absent or undecodable.
func rfc2231Param(value, name string) string {
	type piece struct {
		n       int
		encoded bool
		value   string
	}
	var pieces []piece
	single := ""
	haveSingle := false
	for _, param := range splitParams(value) {
		key, val, ok := strings.Cut(param, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = unquoteMIME(val[1 : len(val)-1])
		}
		if key == name+"*" {
			single, haveSingle = val, true
			continue
		}
		rest, found := strings.CutPrefix(key, name+"*")
		if !found {
			continue
		}
		encoded := strings.HasSuffix(rest, "*")
		n, err := strconv.Atoi(strings.TrimSuffix(rest, "*"))
		if err != nil || n < 0 || n > 999 {
			continue
		}
		pieces = append(pieces, piece{n: n, encoded: encoded, value: val})
	}

	var charset string
	var raw strings.Builder
	switch {
	case haveSingle:
		cs, _, text, ok := splitExtValue(single)
		if !ok {
			return ""
		}
		charset = cs
		raw.WriteString(percentDecode(text))
	case len(pieces) > 0:
		sort.Slice(pieces, func(i, j int) bool { return pieces[i].n < pieces[j].n })
		for i, p := range pieces {
			if p.n != i {
				break
			}
			text := p.value
			if i == 0 && p.encoded {
				cs, _, t, ok := splitExtValue(text)
				if !ok {
					return ""
				}
				charset, text = cs, t
			}
			if p.encoded {
				text = percentDecode(text)
			}
			raw.WriteString(text)
		}
	default:
		return ""
	}
	if charset == "" {
		return strings.ToValidUTF8(raw.String(), "�")
	}
	return decodeCharset([]byte(raw.String()), charset)
}

// splitParams splits a header value at the semicolons outside quotes,
// dropping the leading media type or disposition.
func splitParams(value string) []string {
	var out []string
	var cur strings.Builder
	inQuote, escaped := false, false
	for _, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case r == ';' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	out = append(out, cur.String())
	if len(out) > 0 {
		out = out[1:]
	}
	return out
}

// unquoteMIME undoes a quoted-string's backslash escapes.
func unquoteMIME(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func splitExtValue(v string) (charset, lang, text string, ok bool) {
	parts := strings.SplitN(v, "'", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// percentDecode undoes RFC 2231's %XX, keeping a malformed escape as it is.
func percentDecode(s string) string {
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// decodeTransfer undoes a part's Content-Transfer-Encoding: quoted-printable,
// base64 (line breaks, stray characters and missing padding tolerated), or
// none for 7bit, 8bit, binary and anything unknown.
func decodeTransfer(cte string, body io.Reader) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(body))
	case "base64":
		raw, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		return decodeBase64(raw)
	default:
		return io.ReadAll(body)
	}
}

func decodeBase64(raw []byte) ([]byte, error) {
	clean := make([]byte, 0, len(raw))
	for _, c := range raw {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			clean = append(clean, c)
		}
	}
	if len(clean)%4 == 1 {
		clean = clean[:len(clean)-1]
	}
	return base64.RawStdEncoding.DecodeString(string(clean))
}

// decodeCharset decodes content from charset to UTF-8; an unknown charset is
// read as UTF-8, and invalid bytes are replaced either way.
func decodeCharset(content []byte, charset string) string {
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	if charset != "" {
		if enc, err := lookupCharset(charset); err == nil {
			if decoded, err := enc.NewDecoder().Bytes(content); err == nil {
				return strings.ToValidUTF8(string(decoded), "�")
			}
		}
	}
	return strings.ToValidUTF8(string(content), "�")
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

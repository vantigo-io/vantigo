package peppol

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The two document type identifiers that answer "can this customer receive an
// EHF invoice / credit note?": Peppol BIS Billing 3.0, which is what EHF 3.0 is
// in Peppol terms.
//
// They are compared as whole strings and nothing looser — not a prefix, not a
// substring, not a wildcard — and two participants seen live on 2026-09-21 are
// why:
//
//   - DFØ publishes the EHF *Reminder* profile, whose identifier is the
//     invoice identifier with "#conformant#urn:fdc:anskaffelser.no:2019:ehf:
//     reminder:3.0::2.2" in place of the version. A prefix or substring match
//     would report a reminder-only receiver as able to receive invoices;
//   - IBM Norge and Dell AS resolve, answer 200, and register order or
//     response profiles only — no invoice document type at all. Anything that
//     inferred "registered, therefore invoiceable" would be wrong about them.
//
// The "peppol-doctype-wildcard" PINT identifiers are deliberately not
// accepted: Norway is not on PINT, a PINT specialisation is another country's
// format, and every Norwegian wildcard registrant measured (12,947 of 12,947)
// also publishes the exact identifier below, so accepting the wildcard buys
// nothing and risks sending a document in a profile the receiver does not
// actually take.
const (
	invoiceDocumentType = "busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice" +
		"##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1"

	creditNoteDocumentType = "busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2::CreditNote" +
		"##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1"
)

const (
	// servicesSegment separates the participant identifier from the document
	// type identifier in a ServiceMetadataReference href.
	servicesSegment = "/services/"

	// maxSMPBody caps the response body. A ServiceGroup with a few hundred
	// references is tens of kilobytes; a megabyte is room to spare and still a
	// bound on what a hostile or broken SMP can make this server allocate.
	maxSMPBody = 1 << 20
)

// ErrSMPURL is every base URL this package refuses to fetch. The URL comes out
// of a DNS record published by whoever runs the participant's SMP, so it is
// third-party input: the specification says SMP access is plain https on port
// 443, and anything else is either a mistake or an attempt to aim this
// server's HTTP client somewhere it should not go.
var ErrSMPURL = errors.New("peppol: unusable SMP base URL")

// smpRequestURL is the one request this package makes of an SMP: the
// ServiceGroup of a participant, as
//
//	<base>/iso6523-actorid-upis%3A%3A<value>
//
// The base URL is validated first (see ErrSMPURL): it must parse, be https,
// carry no userinfo, name a host, use port 443 or none, and carry no query or
// fragment. Trailing slashes are trimmed because the base may or may not end
// in one — both were seen live, and ELMA answers 400 to the "//" that would
// otherwise result.
func smpRequestURL(base, participant string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, fmt.Errorf("%w: %q could not be parsed: %w", ErrSMPURL, base, err)
	}
	switch {
	case parsed.Scheme != "https":
		return nil, fmt.Errorf("%w: %q is not https", ErrSMPURL, base)
	case parsed.Host == "":
		return nil, fmt.Errorf("%w: %q names no host", ErrSMPURL, base)
	case parsed.User != nil:
		return nil, fmt.Errorf("%w: %q carries userinfo", ErrSMPURL, base)
	case parsed.Port() != "" && parsed.Port() != "443":
		return nil, fmt.Errorf("%w: %q uses port %s, and SMP access is https on 443", ErrSMPURL, base, parsed.Port())
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return nil, fmt.Errorf("%w: %q carries a query or a fragment", ErrSMPURL, base)
	}

	// The escaped identifier is spliced in as a raw path rather than assigned
	// to Path, so that the colons stay percent-encoded on the wire: url.URL
	// would otherwise re-encode Path with ":" left bare, which is legal in a
	// URL but is not what the SMPs accept.
	target, err := url.Parse(parsed.String() + "/" + escapeIdentifier(Scheme+"::"+participant))
	if err != nil {
		return nil, fmt.Errorf("%w: %q could not be turned into a request URL: %w", ErrSMPURL, base, err)
	}
	return target, nil
}

// escapeIdentifier percent-encodes a Peppol identifier for use as one path
// segment. url.PathEscape leaves ":" alone — legal in a path segment, but the
// SMPs expect "%3A" — so the colons are forced afterwards; "#" is already
// escaped by PathEscape and must be, since it would otherwise start a fragment
// and truncate the identifier.
func escapeIdentifier(identifier string) string {
	return strings.ReplaceAll(url.PathEscape(identifier), ":", "%3A")
}

// fetchDocumentTypes performs the ServiceGroup GET and returns the document
// type identifiers the participant has registered at this SMP.
//
// found is false for a 404 and only for a 404: that is the SMP's own
// definitive "I publish nothing for this participant", the same answer as
// NXDOMAIN one step earlier. Every other non-2xx status is an error naming the
// status — never the body, which is the SMP's error page and not ours to
// repeat in a log or a problem response.
func (c *Client) fetchDocumentTypes(ctx context.Context, target *url.URL) (bool, []string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false, nil, fmt.Errorf("peppol: the SMP request could not be built: %w", err)
	}
	// No Accept header, deliberately: ELMA answers 406 to
	// "Accept: application/xml", and Go adds none of its own. The response is
	// XML because that is the only thing an SMP answers with.

	response, err := c.http.Do(request)
	if err != nil {
		return false, nil, fmt.Errorf("peppol: the SMP at %s could not be reached: %w", target.Host, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		return false, nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return false, nil, fmt.Errorf("peppol: the SMP at %s answered %d", target.Host, response.StatusCode)
	}

	// One byte past the cap is read so that a body exactly at the limit is
	// still accepted and one over it is refused rather than silently truncated
	// into a document that happens to parse.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSMPBody+1))
	if err != nil {
		return false, nil, fmt.Errorf("peppol: the SMP at %s could not be read: %w", target.Host, err)
	}
	if len(body) > maxSMPBody {
		return false, nil, fmt.Errorf("peppol: the SMP at %s answered with a body larger than %d bytes", target.Host, maxSMPBody)
	}

	documentTypes, err := documentTypesFrom(body)
	if err != nil {
		return false, nil, fmt.Errorf("peppol: the SMP at %s: %w", target.Host, err)
	}
	return true, documentTypes, nil
}

// serviceGroup is the part of an SMP ServiceGroup this package reads: the
// references, for their href alone. The hrefs are never fetched — one GET
// answers the capability question, and following a few hundred links per
// lookup would turn a question into a load test.
type serviceGroup struct {
	XMLName    xml.Name `xml:"http://busdox.org/serviceMetadata/publishing/1.0/ ServiceGroup"`
	References []struct {
		Href string `xml:"href,attr"`
	} `xml:"http://busdox.org/serviceMetadata/publishing/1.0/ ServiceMetadataReferenceCollection>ServiceMetadataReference"`
}

// documentTypesFrom reads the registered document type identifiers out of a
// ServiceGroup document: the path segment after "/services/" in each
// reference's href, percent-decoded.
//
// A body that is not a ServiceGroup in the publishing namespace is an error —
// an SMP answering 200 with somebody's HTML error page must not read as "this
// participant has registered nothing". An individual href that does not name a
// document type (no "/services/" segment, or an undecodable one) is skipped:
// it is not a capability, and one odd link is no reason to fail the lookup.
func documentTypesFrom(body []byte) ([]string, error) {
	var parsed serviceGroup
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("answered with something that is not a ServiceGroup: %w", err)
	}

	var documentTypes []string
	for _, reference := range parsed.References {
		index := strings.LastIndex(reference.Href, servicesSegment)
		if index < 0 {
			continue
		}
		identifier, err := url.PathUnescape(reference.Href[index+len(servicesSegment):])
		if err != nil || identifier == "" {
			continue
		}
		documentTypes = append(documentTypes, identifier)
	}
	return documentTypes, nil
}

// capabilities is the allow-list comparison: whole-string equality against the
// invoice and credit note identifiers, in one pass over what the participant
// registered. See the constants' comment for why nothing looser will do.
func capabilities(documentTypes []string) (invoice, creditNote bool) {
	for _, documentType := range documentTypes {
		switch documentType {
		case invoiceDocumentType:
			invoice = true
		case creditNoteDocumentType:
			creditNote = true
		}
	}
	return invoice, creditNote
}

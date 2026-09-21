// Package peppol answers one question about a business: can it receive a
// Peppol BIS Billing 3.0 (EHF) invoice?
//
// The answer is not ours to guess. It lives in the Peppol network, and the
// only honest way to read it is the way the network itself is specified
// (Digdir says as much: the only valid lookup is "the PEPPOL way … via the
// SML"):
//
//  1. hash the participant identifier's VALUE into a DNS name under the SML
//     zone (ParticipantHost);
//  2. ask that name for its NAPTR records, which carry the URL of the
//     participant's Service Metadata Publisher (Resolver.LookupNAPTR);
//  3. ask that SMP, with one unauthenticated GET, which document types the
//     participant has registered, and look for the invoice and credit-note
//     ids exactly (Client.Lookup).
//
// Every step has three outcomes, never two, and the middle one is the one
// worth naming: the participant is registered, the participant is definitely
// not registered (NXDOMAIN, or the SMP answering 404), or we could not find
// out (a timeout, SERVFAIL, REFUSED, a non-2xx SMP response, a body that will
// not parse). A failure must never be reported as "not registered" — it is
// the difference between "do not send an EHF invoice to this customer" and
// "try again later", and telling them apart is most of what this package is
// for.
//
// Two implementation choices are deliberate and worth keeping:
//
// The DNS query is built and parsed here, on golang.org/x/net/dns/dnsmessage
// (already in the module graph), rather than by pulling in a DNS library or
// asking a public DNS-over-HTTPS resolver. NAPTR is RR type 35, which the Go
// resolver does not expose at all, so something has to speak the wire format;
// doing it here adds no third-party dependency, and it keeps the lookups on
// the operator's own resolver instead of telling a third party which
// organisation numbers a tenant is asking about.
//
// The SMP base URL is third-party data — it arrives in a DNS record published
// by whoever runs the participant's SMP — so it is validated before use
// (https, no userinfo, port 443) and fetched through a transport that dials
// via internal/netguard, which refuses private, loopback, link-local, CGNAT
// and metadata addresses and connects to the very address it checked. A
// hostile or compromised SMP record must not turn this lookup into a probe of
// the network the server runs in.
//
// The package is pure network plumbing: no database, no HTTP contract, no
// module. Callers (Customers today, Invoices later) hold a *Client built from
// configuration and hand it a participant identifier value such as
// "0192:923609016".
package peppol

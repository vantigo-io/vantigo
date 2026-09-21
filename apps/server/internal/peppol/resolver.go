package peppol

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// typeNAPTR is RR type 35. dnsmessage has no constant for it and no NAPTR
// resource type, which is the whole reason naptr.go exists: the record arrives
// as a dnsmessage.UnknownResource carrying raw RDATA.
const typeNAPTR = dnsmessage.Type(35)

const (
	// defaultResolvConf is where the server's own name servers are configured.
	defaultResolvConf = "/etc/resolv.conf"

	// dnsPort is the port a name server is assumed to listen on when the
	// configured address names none.
	dnsPort = "53"

	// attemptTimeout bounds one query to one server. Three seconds is the
	// conventional resolver timeout, and it has to be well inside one
	// Client.Lookup's whole budget (10s by default) with room for the SMP
	// request afterwards.
	attemptTimeout = 3 * time.Second

	// attemptsPerServer is how many times each server is asked before the next
	// one is tried. Two, because the common failure over UDP is a lost
	// datagram and the cheapest fix for that is to ask again.
	attemptsPerServer = 2

	// udpReadSize is the read buffer for a UDP answer. Without EDNS0 a server
	// must not send more than 512 bytes over UDP and must set the TC bit
	// instead; the buffer is larger than that so a server that ignores the
	// rule produces a parse failure (and a retry) rather than a silently
	// half-read message.
	udpReadSize = 4096

	// maxTCPMessage caps a TCP answer. The two-byte length prefix already
	// bounds one message at 65535 bytes, so this is the explicit ceiling
	// rather than a limit that binds — it is here so that no future change to
	// the framing can turn a hostile length into an unbounded allocation.
	maxTCPMessage = 64 << 10

	// maxAliasHops bounds a CNAME chain inside one answer. Anything longer is
	// a misconfiguration or a loop, and either way not something to follow.
	maxAliasHops = 8
)

// errNoDNSServer is the answer when there is nowhere to ask: no server was
// configured and /etc/resolv.conf named none (or could not be read).
//
// There is deliberately no fallback to a public resolver. Falling back to
// 8.8.8.8 or 1.1.1.1 would make this lookup work on a misconfigured host at
// the price of telling a third party which organisation numbers a tenant is
// asking about — which is one of the reasons this package resolves NAPTR
// itself instead of using a DNS-over-HTTPS provider.
var errNoDNSServer = errors.New("peppol: no DNS server configured")

// Resolver performs the one DNS query Peppol discovery needs: the NAPTR
// records of a participant host.
//
// Servers are "host" or "host:port" addresses (port 53 is assumed); when it is
// empty the server's own name servers are read from /etc/resolv.conf. Timeout
// bounds ONE attempt — two attempts per server, servers in order — and
// defaults to attemptTimeout; the caller's context bounds the lookup as a
// whole, and an attempt never outlives it.
//
// The zero value is usable and asks the host's configured name servers.
type Resolver struct {
	Servers []string
	Timeout time.Duration

	// resolvConf overrides defaultResolvConf. It is a test seam, not
	// configuration: an operator who wants a particular resolver sets
	// PEPPOL_DNS_SERVER, which arrives as Servers.
	resolvConf string
}

// LookupNAPTR asks for host's NAPTR records.
//
// The three outcomes are the point of the signature:
//
//   - records, true, nil — the name exists and these are its NAPTR records;
//   - nil, false, nil — the participant is NOT in the network. NXDOMAIN says
//     so outright, and so does NOERROR with no NAPTR records: the SML
//     publishes a name only for a registered participant, so a name without
//     NAPTR records has no SMP behind it and cannot be asked anything. Both
//     are definitive answers a caller may act on;
//   - nil, false, err — we could not find out. A timeout, SERVFAIL, REFUSED,
//     a reply that does not answer the question asked, or a NAPTR record whose
//     RDATA will not decode. This must never be reported to a user as "not
//     registered".
func (r *Resolver) LookupNAPTR(ctx context.Context, host string) ([]NAPTR, bool, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, false, fmt.Errorf("peppol: %q is not a usable DNS name: %w", host, err)
	}
	question := dnsmessage.Question{Name: name, Type: typeNAPTR, Class: dnsmessage.ClassINET}

	servers, err := r.serverAddresses()
	if err != nil {
		return nil, false, err
	}

	var lastErr error
	for _, server := range servers {
		for range attemptsPerServer {
			records, found, err := r.attempt(ctx, server, question)
			if err == nil {
				return records, found, nil
			}
			lastErr = fmt.Errorf("peppol: NAPTR lookup for %s via %s: %w", host, server, err)
			// A context that is already done will not let the next attempt do
			// anything but fail the same way, only slower.
			if ctx.Err() != nil {
				return nil, false, lastErr
			}
		}
	}
	return nil, false, lastErr
}

// attempt performs one exchange with one server: a UDP query, retried over TCP
// against the same server if the answer came back truncated.
func (r *Resolver) attempt(ctx context.Context, server string, question dnsmessage.Question) ([]NAPTR, bool, error) {
	id := queryID()
	query, err := packQuery(id, question)
	if err != nil {
		return nil, false, err
	}

	ctx, cancel := r.attemptContext(ctx)
	defer cancel()

	answer, err := exchangeUDP(ctx, server, query)
	if err != nil {
		return nil, false, err
	}
	records, found, truncated, err := readReply(answer, id, question)
	if err != nil || !truncated {
		return records, found, err
	}

	// The TC bit means the server had more to say than fits in a datagram.
	// Several NAPTR records with long URLs get there easily, and a resolver
	// that stopped here would report a registered participant as having no SMP.
	if answer, err = exchangeTCP(ctx, server, query); err != nil {
		return nil, false, err
	}
	records, found, truncated, err = readReply(answer, id, question)
	if err == nil && truncated {
		return nil, false, errors.New("the answer was still truncated over TCP")
	}
	return records, found, err
}

// attemptContext bounds one attempt by r.Timeout (or attemptTimeout), never
// past the deadline the caller's context already carries.
func (r *Resolver) attemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = attemptTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// serverAddresses is the list of name servers to ask, each as host:port.
func (r *Resolver) serverAddresses() ([]string, error) {
	if len(r.Servers) > 0 {
		servers := make([]string, 0, len(r.Servers))
		for _, server := range r.Servers {
			servers = append(servers, withDNSPort(server))
		}
		return servers, nil
	}

	path := r.resolvConf
	if path == "" {
		path = defaultResolvConf
	}
	servers, err := nameserversFrom(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %q could not be read: %w", errNoDNSServer, path, err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("%w: %q names no nameserver", errNoDNSServer, path)
	}
	return servers, nil
}

// nameserversFrom reads the "nameserver" lines of a resolv.conf-shaped file,
// in order, each as host:port. Everything else in the file — search, domain,
// options, sortlist, comments — is ignored: this resolver sends one absolute
// query for a name it built itself, so a search list would only get in the way.
func nameserversFrom(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var servers []string
	for line := range strings.Lines(string(contents)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		servers = append(servers, withDNSPort(fields[1]))
	}
	return servers, nil
}

// withDNSPort adds the default DNS port to an address that names none, and
// brackets a bare IPv6 literal on the way (net.SplitHostPort rejects one,
// which is exactly how a missing port is recognised).
func withDNSPort(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(server, dnsPort)
}

// queryID is a fresh message id, from crypto/rand rather than math/rand: the
// id (with the connected socket's source port) is all that stops an off-path
// attacker's forged datagram from being accepted as the answer, so it must not
// come from a predictable stream.
func queryID() uint16 {
	var id [2]byte
	// crypto/rand.Read is documented never to fail.
	_, _ = crand.Read(id[:])
	return binary.BigEndian.Uint16(id[:])
}

// packQuery builds the query message: one question, recursion desired (walking
// the delegation is the configured resolver's job, not ours), no EDNS0 — this
// answer is small and the TC-then-TCP fallback covers the case where it is not.
func packQuery(id uint16, question dnsmessage.Question) ([]byte, error) {
	message := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{question},
	}
	packed, err := message.Pack()
	if err != nil {
		return nil, fmt.Errorf("peppol: the NAPTR query could not be packed: %w", err)
	}
	return packed, nil
}

// exchangeUDP sends query to server and returns the first datagram whose
// message id matches. A datagram with any other id is somebody else's answer
// or a spoofing attempt, and is dropped without ending the attempt — the
// deadline, not the first packet to arrive, is what ends the read.
func exchangeUDP(ctx context.Context, server string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := setDeadline(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}

	buf := make([]byte, udpReadSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if n >= 2 && buf[0] == query[0] && buf[1] == query[1] {
			return buf[:n], nil
		}
	}
}

// exchangeTCP sends query to server over TCP, where a message is framed by a
// two-byte big-endian length prefix (RFC 1035 §4.2.2).
func exchangeTCP(ctx context.Context, server string, query []byte) ([]byte, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err := setDeadline(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(query)))); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}

	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(length[:]))
	if size > maxTCPMessage {
		return nil, fmt.Errorf("the answer announces %d bytes, more than the %d this resolver reads", size, maxTCPMessage)
	}
	answer := make([]byte, size)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// setDeadline puts the attempt context's deadline on the connection, so a
// server that accepts a packet and then says nothing cannot hold the read open.
func setDeadline(ctx context.Context, conn net.Conn) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	return conn.SetDeadline(deadline)
}

// readReply validates an answer and decodes its NAPTR records. truncated is
// true when the server set the TC bit, which is the caller's signal to ask the
// same server again over TCP; nothing else has been read from the message in
// that case.
func readReply(answer []byte, id uint16, question dnsmessage.Question) (records []NAPTR, found, truncated bool, err error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(answer)
	if err != nil {
		return nil, false, false, fmt.Errorf("the answer could not be parsed: %w", err)
	}
	if header.ID != id {
		return nil, false, false, fmt.Errorf("the answer has id %d, not this query's %d", header.ID, id)
	}
	if !header.Response {
		return nil, false, false, errors.New("the message is not a response")
	}
	if header.Truncated {
		return nil, false, true, nil
	}

	echoed, err := parser.Question()
	if err != nil {
		return nil, false, false, fmt.Errorf("the answer's question could not be parsed: %w", err)
	}
	// A reply that echoes a different question is not an answer to ours,
	// whatever else it contains. Names are compared case-insensitively because
	// DNS preserves the case a query used but does not honour it.
	if !strings.EqualFold(echoed.Name.String(), question.Name.String()) || echoed.Type != question.Type || echoed.Class != question.Class {
		return nil, false, false, fmt.Errorf("the answer echoes the question %s %d %s, not %s %d %s",
			echoed.Name.String(), echoed.Type, echoed.Class, question.Name.String(), question.Type, question.Class)
	}

	switch header.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		// NXDOMAIN: the name does not exist, so the participant is not in the
		// network. This is the definitive negative the whole design rests on.
		return nil, false, false, nil
	default:
		return nil, false, false, fmt.Errorf("the server answered %v", header.RCode)
	}

	// Any further question (there is never one) has to be stepped over before
	// the parser will move on to the answer section.
	if err := parser.SkipAllQuestions(); err != nil {
		return nil, false, false, fmt.Errorf("the answer's question section could not be read: %w", err)
	}

	byName, aliases, err := readAnswers(&parser)
	if err != nil {
		return nil, false, false, err
	}
	records, err = recordsFor(question.Name.String(), byName, aliases)
	if err != nil {
		return nil, false, false, err
	}
	// NOERROR with no NAPTR records for the name: see LookupNAPTR's doc
	// comment — a name without an SMP record is not a participant we can ask
	// anything, so it is reported as not registered rather than as an empty
	// success.
	return records, records != nil, false, nil
}

// readAnswers decodes the answer section into the NAPTR records per owner name
// and the CNAME aliases between them. Records of other types and classes are
// skipped: an answer section may carry more than what was asked for, and a
// record for another name or type says nothing about this question.
func readAnswers(parser *dnsmessage.Parser) (map[string][]NAPTR, map[string]string, error) {
	byName := map[string][]NAPTR{}
	aliases := map[string]string{}
	for {
		header, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return byName, aliases, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("an answer record's header could not be parsed: %w", err)
		}
		if header.Class != dnsmessage.ClassINET {
			if err := parser.SkipAnswer(); err != nil {
				return nil, nil, fmt.Errorf("an answer record could not be skipped: %w", err)
			}
			continue
		}
		name := canonicalName(header.Name.String())

		switch header.Type {
		case typeNAPTR:
			body, err := parser.UnknownResource()
			if err != nil {
				return nil, nil, fmt.Errorf("the NAPTR record for %s could not be read: %w", name, err)
			}
			record, err := decodeNAPTR(body.Data)
			if err != nil {
				return nil, nil, fmt.Errorf("the NAPTR record for %s: %w", name, err)
			}
			byName[name] = append(byName[name], record)
		case dnsmessage.TypeCNAME:
			body, err := parser.CNAMEResource()
			if err != nil {
				return nil, nil, fmt.Errorf("the CNAME record for %s could not be read: %w", name, err)
			}
			aliases[name] = canonicalName(body.CNAME.String())
		default:
			if err := parser.SkipAnswer(); err != nil {
				return nil, nil, fmt.Errorf("an answer record could not be skipped: %w", err)
			}
		}
	}
}

// recordsFor picks out the records belonging to the name that was asked about,
// following CNAMEs inside the answer section. It never sends another query: if
// the server aliased the name, it is the server's job to have included the
// target's records (they always are, in the same answer), and a chain that
// leads nowhere is a failure rather than an absence — the participant may well
// be registered behind that alias.
//
// A name with no alias and no records is not an error: that is the NOERROR/no
// data case readReply reports as "not registered".
func recordsFor(qname string, byName map[string][]NAPTR, aliases map[string]string) ([]NAPTR, error) {
	target := canonicalName(qname)
	for hop := 0; ; hop++ {
		if records := byName[target]; records != nil {
			return records, nil
		}
		next, aliased := aliases[target]
		if !aliased {
			if hop == 0 {
				return nil, nil
			}
			return nil, fmt.Errorf("the answer aliases %s to %s but carries none of its NAPTR records", canonicalName(qname), target)
		}
		if hop >= maxAliasHops {
			return nil, fmt.Errorf("the answer's alias chain from %s is longer than %d hops", canonicalName(qname), maxAliasHops)
		}
		target = next
	}
}

// canonicalName lower-cases a name for comparison. DNS names are
// case-insensitive but a server echoes back whatever case it was given, so
// nothing may be matched on the case it arrived in.
func canonicalName(name string) string {
	return strings.ToLower(name)
}

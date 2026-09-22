package peppol

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Everything in this file talks to an in-process name server on 127.0.0.1 —
// real sockets, real wire format, no real DNS. That is deliberate: the parts
// of this resolver most likely to be wrong are the parts a mock would hide
// (the UDP/TCP framing, the deadline, the retry), so the stub answers with
// bytes rather than with a Go value. The stub binds UDP and TCP on the SAME
// port, because that is what a name server does and what the truncation
// fallback needs.

// stubQuery is one query the stub name server saw.
type stubQuery struct {
	transport string // "udp" or "tcp"
	message   []byte
}

// stubDNS is a name server that answers whatever a test tells it to. handle
// returns the datagrams (or, over TCP, the messages) to send back: none at
// all models a server that does not answer, and more than one models a stray
// or spoofed packet arriving before the real answer.
type stubDNS struct {
	addr   string
	handle func(query []byte, transport string) [][]byte

	mu      sync.Mutex
	queries []stubQuery
}

// newStubDNS starts the stub and stops it when the test ends.
func newStubDNS(t *testing.T, handle func(query []byte, transport string) [][]byte) *stubDNS {
	t.Helper()
	udp, tcp := listenSamePort(t)
	s := &stubDNS{addr: tcp.Addr().String(), handle: handle}
	done := make(chan struct{}, 2)
	go func() { s.serveUDP(udp); done <- struct{}{} }()
	go func() { s.serveTCP(tcp); done <- struct{}{} }()
	t.Cleanup(func() {
		_ = udp.Close()
		_ = tcp.Close()
		<-done
		<-done
	})
	return s
}

// listenSamePort opens a UDP socket and a TCP listener on one 127.0.0.1 port.
// The port is picked by the kernel for UDP and then asked for by name on TCP,
// which occasionally loses a race with another process, so it retries.
func listenSamePort(t *testing.T) (net.PacketConn, net.Listener) {
	t.Helper()
	for range 20 {
		udp, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket: %v", err)
		}
		port := udp.LocalAddr().(*net.UDPAddr).Port
		tcp, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return udp, tcp
		}
		_ = udp.Close()
	}
	t.Fatal("could not bind the same port on UDP and TCP after 20 tries")
	return nil, nil
}

func (s *stubDNS) serveUDP(conn net.PacketConn) {
	buf := make([]byte, 4096)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		for _, reply := range s.answer(buf[:n], "udp") {
			if _, err := conn.WriteTo(reply, from); err != nil {
				return
			}
		}
	}
}

func (s *stubDNS) serveTCP(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		func() {
			defer func() { _ = conn.Close() }()
			var length [2]byte
			if _, err := io.ReadFull(conn, length[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(length[:]))
			if _, err := io.ReadFull(conn, query); err != nil {
				return
			}
			for _, reply := range s.answer(query, "tcp") {
				framed := binary.BigEndian.AppendUint16(nil, uint16(len(reply)))
				if _, err := conn.Write(append(framed, reply...)); err != nil {
					return
				}
			}
		}()
	}
}

// answer records the query and asks the test's handler what to send back.
func (s *stubDNS) answer(query []byte, transport string) [][]byte {
	s.mu.Lock()
	s.queries = append(s.queries, stubQuery{transport: transport, message: append([]byte(nil), query...)})
	s.mu.Unlock()
	return s.handle(append([]byte(nil), query...), transport)
}

// seen returns the queries the stub has answered so far.
func (s *stubDNS) seen() []stubQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubQuery(nil), s.queries...)
}

// count is how many queries arrived over transport.
func (s *stubDNS) count(transport string) int {
	n := 0
	for _, q := range s.seen() {
		if q.transport == transport {
			n++
		}
	}
	return n
}

// pollUntilCount waits for stub's count(transport) to reach want, instead of
// asserting it the instant LookupNAPTR returns: a UDP datagram (or, for the
// TCP goroutine, the Accept that has not yet run) confirms nothing about
// whether the stub's own bookkeeping goroutine has recorded it yet, so an
// immediate exact assert here is racing that goroutine's own scheduling, not
// the resolver. It still fails exactly like an exact assert would: past
// want, or once the deadline passes short of it.
func pollUntilCount(t *testing.T, stub *stubDNS, transport string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := stub.count(transport)
		if n == want {
			return
		}
		if n > want || time.Now().After(deadline) {
			t.Fatalf("the stub saw %d %s queries, want %d", n, transport, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// reply describes the answer a test wants the stub to send. The zero value is
// a NOERROR response echoing the query's question with no answers.
type reply struct {
	rcode        dnsmessage.RCode
	truncated    bool
	notAResponse bool
	id           *uint16          // nil → the query's own id
	question     *dnsmessage.Name // nil → the query's own name
	questionType *dnsmessage.Type // nil → the query's own type
	answers      []answerRecord
}

// answerRecord is one record to put in the answer section.
type answerRecord struct {
	name  string          // "" → the question's name
	rtype dnsmessage.Type // 0 → NAPTR
	rdata []byte          // NAPTR rdata (rtype NAPTR)
	cname string          // target (rtype CNAME)
	ipv4  string          // address (rtype A)
}

// build packs r into a wire-format reply to query. It runs on the stub's own
// goroutine, so it reports a broken fixture by returning an error rather than
// by failing the test from the wrong goroutine.
func (r reply) build(query []byte) ([]byte, error) {
	var parsed dnsmessage.Message
	if err := parsed.Unpack(query); err != nil {
		return nil, fmt.Errorf("unpacking the query: %w", err)
	}
	if len(parsed.Questions) != 1 {
		return nil, fmt.Errorf("query has %d questions, want 1", len(parsed.Questions))
	}
	question := parsed.Questions[0]
	if r.question != nil {
		question.Name = *r.question
	}
	if r.questionType != nil {
		question.Type = *r.questionType
	}

	id := parsed.ID
	if r.id != nil {
		id = *r.id
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 id,
		Response:           !r.notAResponse,
		RecursionDesired:   true,
		RecursionAvailable: true,
		Truncated:          r.truncated,
		RCode:              r.rcode,
	})
	b.EnableCompression()
	errs := []error{b.StartQuestions(), b.Question(question), b.StartAnswers()}
	for _, a := range r.answers {
		name := question.Name
		if a.name != "" {
			named, err := dnsmessage.NewName(a.name)
			if err != nil {
				return nil, err
			}
			name = named
		}
		header := dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: 60}
		switch a.rtype {
		case dnsmessage.TypeCNAME:
			target, err := dnsmessage.NewName(a.cname)
			if err != nil {
				return nil, err
			}
			errs = append(errs, b.CNAMEResource(header, dnsmessage.CNAMEResource{CNAME: target}))
		case dnsmessage.TypeA:
			errs = append(errs, b.AResource(header, dnsmessage.AResource{A: [4]byte(net.ParseIP(a.ipv4).To4())}))
		default:
			rtype := a.rtype
			if rtype == 0 {
				rtype = typeNAPTR
			}
			errs = append(errs, b.UnknownResource(header, dnsmessage.UnknownResource{Type: rtype, Data: a.rdata}))
		}
	}
	msg, err := b.Finish()
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return msg, nil
}

func mustName(t *testing.T, name string) dnsmessage.Name {
	t.Helper()
	packed, err := dnsmessage.NewName(name)
	if err != nil {
		t.Fatalf("dnsmessage.NewName(%q): %v", name, err)
	}
	return packed
}

// answersWith is the common case: one reply, built from r. A fixture that will
// not build is reported with t.Errorf rather than t.Fatalf — this runs on the
// stub's goroutine, which newStubDNS joins before the test ends.
func answersWith(t *testing.T, r reply) func([]byte, string) [][]byte {
	return func(query []byte, _ string) [][]byte {
		msg, err := r.build(query)
		if err != nil {
			t.Errorf("building a stub reply: %v", err)
			return nil
		}
		return [][]byte{msg}
	}
}

// testResolver points a Resolver at the stub with a short per-attempt timeout,
// so a test that waits for a timeout waits milliseconds rather than the 3s a
// production attempt is allowed. Nothing else about the resolver changes.
func testResolver(server string) *Resolver {
	return &Resolver{Servers: []string{server}, Timeout: 300 * time.Millisecond}
}

const elmaBase = "https://smp.elma-smp.no/"

func elmaRecord() NAPTR {
	return NAPTR{Order: 100, Preference: 10, Flags: "U", Service: smpService, Regexp: "!.*!" + elmaBase + "!", Replacement: "."}
}

func TestLookupNAPTR_ReturnsTheRecordsAndTheQuestionItAsked(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{{rdata: elmaRDATA()}}}))

	host := ParticipantHost(prodZone, "0192:923609016")
	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), host)
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true for a name with NAPTR records")
	}
	if len(records) != 1 || records[0] != elmaRecord() {
		t.Fatalf("records = %+v, want exactly %+v", records, elmaRecord())
	}

	// The question is as much of the contract as the answer: a NAPTR query for
	// the participant host, class IN, with recursion asked for (the operator's
	// resolver is the one that walks the delegation).
	seen := stub.seen()
	if len(seen) != 1 {
		t.Fatalf("the stub saw %d queries, want 1", len(seen))
	}
	var query dnsmessage.Message
	if err := query.Unpack(seen[0].message); err != nil {
		t.Fatalf("unpacking the query: %v", err)
	}
	if query.Response {
		t.Error("the query has the response bit set")
	}
	if !query.RecursionDesired {
		t.Error("the query does not ask for recursion")
	}
	if len(query.Questions) != 1 {
		t.Fatalf("the query has %d questions, want 1", len(query.Questions))
	}
	got := query.Questions[0]
	if want := host + "."; got.Name.String() != want {
		t.Errorf("question name = %q, want %q", got.Name.String(), want)
	}
	if got.Type != dnsmessage.Type(35) {
		t.Errorf("question type = %d, want 35 (NAPTR)", got.Type)
	}
	if got.Class != dnsmessage.ClassINET {
		t.Errorf("question class = %v, want IN", got.Class)
	}
}

// TestLookupNAPTR_NXDOMAINIsNotRegistered pins the one negative the network
// gives us: the name does not exist, so the participant is not in the network.
// It is an answer, not a failure.
func TestLookupNAPTR_NXDOMAINIsNotRegistered(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{rcode: dnsmessage.RCodeNameError}))

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "nobody.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if found || records != nil {
		t.Fatalf("found = %v, records = %+v, want false and none", found, records)
	}
	if n := stub.count("udp"); n != 1 {
		t.Errorf("the stub saw %d queries, want 1 — NXDOMAIN is definitive and must not be retried", n)
	}
}

// TestLookupNAPTR_NoDataIsNotRegistered: the SML publishes a name only for a
// registered participant, so NOERROR with no NAPTR records means the name was
// synthesised by something in the middle, or the registration is gone. Either
// way there is no SMP, and reporting "registered" off the bare existence of a
// name would make the next step (ask the SMP) impossible to take.
func TestLookupNAPTR_NoDataIsNotRegistered(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{}))

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "nobody.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if found || records != nil {
		t.Fatalf("found = %v, records = %+v, want false and none", found, records)
	}
}

// TestLookupNAPTR_FailuresAreErrors is the distinction the whole feature rests
// on: a server that cannot answer is never "not registered".
func TestLookupNAPTR_FailuresAreErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		reply reply
		want  string
	}{
		{"SERVFAIL", reply{rcode: dnsmessage.RCodeServerFailure}, "RCodeServerFailure"},
		{"REFUSED", reply{rcode: dnsmessage.RCodeRefused}, "RCodeRefused"},
		{"FORMERR", reply{rcode: dnsmessage.RCodeFormatError}, "RCodeFormatError"},
		{"NOTIMP", reply{rcode: dnsmessage.RCodeNotImplemented}, "RCodeNotImplemented"},
		{"an answer to somebody else's query", reply{id: idPtr(0xbeef)}, "timeout"},
		{"a query echoed back instead of a response", reply{notAResponse: true}, "response"},
		{"a different question echoed", reply{question: namePtr(t, "elsewhere.example.test.")}, "question"},
		{"a different question type echoed", reply{questionType: typePtr(dnsmessage.TypeA)}, "question"},
		{"a NAPTR record whose rdata is malformed", reply{answers: []answerRecord{{rdata: []byte{0x00, 0x64}}}}, "NAPTR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := newStubDNS(t, answersWith(t, tc.reply))

			records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
			if err == nil {
				t.Fatalf("LookupNAPTR = %+v, %v, want an error", records, found)
			}
			if found {
				t.Error("found = true alongside an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			if n := stub.count("udp"); n != 2 {
				t.Errorf("the stub saw %d queries, want 2 — one server gets two attempts", n)
			}
		})
	}
}

func idPtr(id uint16) *uint16 { return &id }

func namePtr(t *testing.T, name string) *dnsmessage.Name {
	packed := mustName(t, name)
	return &packed
}

func typePtr(rtype dnsmessage.Type) *dnsmessage.Type { return &rtype }

// TestLookupNAPTR_IgnoresAStrayDatagram proves the id is checked rather than
// the first thing on the socket believed: an off-path spoofer's best hope is
// to arrive before the real server, and the id is what shuts that door.
func TestLookupNAPTR_IgnoresAStrayDatagram(t *testing.T) {
	t.Parallel()
	spoofed := reply{id: idPtr(0x1234), rcode: dnsmessage.RCodeNameError}
	genuine := reply{answers: []answerRecord{{rdata: elmaRDATA()}}}
	stub := newStubDNS(t, func(query []byte, _ string) [][]byte {
		first, err := spoofed.build(query)
		second, err2 := genuine.build(query)
		if err := errors.Join(err, err2); err != nil {
			t.Errorf("building a stub reply: %v", err)
			return nil
		}
		return [][]byte{first, second}
	})

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 1 {
		t.Fatalf("found = %v, records = %+v, want the real answer's single record", found, records)
	}
}

// TestLookupNAPTR_TruncatedReplyRetriesOverTCP: a NAPTR set that does not fit
// in 512 bytes is normal enough (several records, long URLs), and a resolver
// that stopped at the TC bit would report a registered participant as having
// no SMP.
func TestLookupNAPTR_TruncatedReplyRetriesOverTCP(t *testing.T) {
	t.Parallel()
	full := reply{answers: []answerRecord{
		{rdata: elmaRDATA()},
		{rdata: naptrRDATA(200, 10, "U", smpService, "!.*!https://backup.example.test/!", ".")},
	}}
	stub := newStubDNS(t, func(query []byte, transport string) [][]byte {
		answer := full
		if transport == "udp" {
			answer = reply{truncated: true}
		}
		msg, err := answer.build(query)
		if err != nil {
			t.Errorf("building a stub reply: %v", err)
			return nil
		}
		return [][]byte{msg}
	})

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 2 {
		t.Fatalf("found = %v, records = %+v, want the two records TCP carried", found, records)
	}
	pollUntilCount(t, stub, "tcp", 1)
	pollUntilCount(t, stub, "udp", 1) // the UDP attempt must not be repeated once TCP answered
}

// TestLookupNAPTR_SilentServerTimesOut uses an injected short per-attempt
// timeout so the test is fast; the point is that the attempt ends by itself
// rather than hanging on the read.
func TestLookupNAPTR_SilentServerTimesOut(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, func([]byte, string) [][]byte { return nil })

	resolver := &Resolver{Servers: []string{stub.addr}, Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, found, err := resolver.LookupNAPTR(context.Background(), "someone.example.test")
	if err == nil {
		t.Fatal("LookupNAPTR succeeded against a server that never answers")
	}
	if found {
		t.Error("found = true alongside an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the lookup took %v, want it bounded by two 50ms attempts", elapsed)
	}
	pollUntilCount(t, stub, "udp", 2)
}

// TestLookupNAPTR_ContextDeadlineBoundsTheWholeLookup: Client.Lookup gives the
// resolver the remaining budget of one end-to-end timeout, so an attempt must
// never outlive the context even when its own timeout is longer.
func TestLookupNAPTR_ContextDeadlineBoundsTheWholeLookup(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, func([]byte, string) [][]byte { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	resolver := &Resolver{Servers: []string{stub.addr}} // the production 3s per attempt

	start := time.Now()
	if _, _, err := resolver.LookupNAPTR(ctx, "someone.example.test"); err == nil {
		t.Fatal("LookupNAPTR succeeded against a server that never answers")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the lookup took %v, want the context's 60ms to end it long before the 3s attempt timeout", elapsed)
	}
}

// TestLookupNAPTR_CancelledContextReturnsPromptly: a cancelled lookup — the
// user navigated away, the HTTP handler's request was abandoned — must not sit
// on a socket until the attempt's own deadline. The connection's deadline cannot
// see a cancellation (only a deadline), so the exchange watches ctx.Done()
// itself.
func TestLookupNAPTR_CancelledContextReturnsPromptly(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, func([]byte, string) [][]byte { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	// The production per-attempt timeout (3s) is deliberately left in place: the
	// cancellation, not the deadline, is what must end this.
	resolver := &Resolver{Servers: []string{stub.addr}}
	start := time.Now()
	if _, _, err := resolver.LookupNAPTR(ctx, "someone.example.test"); err == nil {
		t.Fatal("LookupNAPTR succeeded on a cancelled context")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("LookupNAPTR took %v after a cancellation at 50ms, want it to return promptly rather than at the 3s attempt deadline", elapsed)
	}
}

// TestLookupNAPTR_TriesEveryServerInOrder: a resolver list is a fallback list.
func TestLookupNAPTR_TriesEveryServerInOrder(t *testing.T) {
	t.Parallel()
	broken := newStubDNS(t, answersWith(t, reply{rcode: dnsmessage.RCodeServerFailure}))
	working := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{{rdata: elmaRDATA()}}}))

	resolver := &Resolver{Servers: []string{broken.addr, working.addr}, Timeout: 300 * time.Millisecond}
	records, found, err := resolver.LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 1 {
		t.Fatalf("found = %v, records = %+v, want the second server's answer", found, records)
	}
	if n := broken.count("udp"); n != 2 {
		t.Errorf("the failing server saw %d queries, want 2 before moving on", n)
	}
	if n := working.count("udp"); n != 1 {
		t.Errorf("the working server saw %d queries, want 1", n)
	}
}

// TestLookupNAPTR_IgnoresRecordsForOtherNamesAndTypes: an answer section may
// carry more than what was asked for (a helpfully cached sibling, an A record
// for the SMP), and picking those up would decode nonsense as a NAPTR.
func TestLookupNAPTR_IgnoresRecordsForOtherNamesAndTypes(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{
		{name: "elsewhere.example.test.", rdata: naptrRDATA(1, 1, "U", smpService, "!.*!https://elsewhere.example.test/!", ".")},
		{rtype: dnsmessage.TypeA, ipv4: "203.0.113.5"},
		{rtype: dnsmessage.Type(99), rdata: []byte{0xff, 0xff}},
		{rdata: elmaRDATA()},
	}}))

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 1 || records[0] != elmaRecord() {
		t.Fatalf("records = %+v, want only the queried name's record %+v", records, elmaRecord())
	}
}

// TestLookupNAPTR_FollowsACNAMEInsideTheAnswer: some middleboxes and some
// hosting setups alias the participant name. Following the alias is fine as
// long as the answer already carries the target's records; re-querying is not
// this resolver's job.
func TestLookupNAPTR_FollowsACNAMEInsideTheAnswer(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{
		{rtype: dnsmessage.TypeCNAME, cname: "alias.example.test."},
		{name: "alias.example.test.", rdata: elmaRDATA()},
	}}))

	records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 1 || records[0] != elmaRecord() {
		t.Fatalf("records = %+v, want the alias target's record %+v", records, elmaRecord())
	}
}

// TestLookupNAPTR_CNAMEWithoutTheTargetsRecordsIsAnError: an alias whose
// target's records are missing is a failure to report, not an absence to act
// on — the participant may well be registered behind that name.
func TestLookupNAPTR_CNAMEWithoutTheTargetsRecordsIsAnError(t *testing.T) {
	t.Parallel()
	cases := map[string][]answerRecord{
		"a dangling alias": {
			{rtype: dnsmessage.TypeCNAME, cname: "alias.example.test."},
		},
		"an alias loop": {
			{rtype: dnsmessage.TypeCNAME, cname: "alias.example.test."},
			{name: "alias.example.test.", rtype: dnsmessage.TypeCNAME, cname: "someone.example.test."},
		},
	}
	for name, answers := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stub := newStubDNS(t, answersWith(t, reply{answers: answers}))

			records, found, err := testResolver(stub.addr).LookupNAPTR(context.Background(), "someone.example.test")
			if err == nil {
				t.Fatalf("LookupNAPTR = %+v, %v, want an error", records, found)
			}
			if found {
				t.Error("found = true alongside an error")
			}
		})
	}
}

// TestLookupNAPTR_RejectsAnImpossibleName: a zone from configuration could be
// anything, and a name too long to pack must fail before a socket is opened.
func TestLookupNAPTR_RejectsAnImpossibleName(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{}))

	host := ParticipantHost(strings.Repeat("verylonglabel.", 30), "0192:923609016")
	if _, _, err := testResolver(stub.addr).LookupNAPTR(context.Background(), host); err == nil {
		t.Fatal("LookupNAPTR accepted a name that cannot be packed")
	}
	if n := len(stub.seen()); n != 0 {
		t.Errorf("the stub saw %d queries, want none", n)
	}
}

// TestLookupNAPTR_ReadsResolvConf covers the production path where no DNS
// server is configured: the server's own resolver is used, read from
// /etc/resolv.conf (the path is injected here — no test reads the real file).
func TestLookupNAPTR_ReadsResolvConf(t *testing.T) {
	t.Parallel()
	stub := newStubDNS(t, answersWith(t, reply{answers: []answerRecord{{rdata: elmaRDATA()}}}))

	// The stub does not listen on port 53, so the file names it with its port;
	// the default-port-53 rule is pinned by TestNameserversFrom instead, where
	// no packet is sent anywhere (a test must not query 127.0.0.1:53 — that
	// may well be the machine's real resolver).
	contents := "# a comment\nsearch example.test\noptions edns0\nnameserver " + stub.addr + "\n"
	resolver := &Resolver{Timeout: 300 * time.Millisecond, resolvConf: writeResolvConf(t, contents)}

	records, found, err := resolver.LookupNAPTR(context.Background(), "someone.example.test")
	if err != nil {
		t.Fatalf("LookupNAPTR: %v", err)
	}
	if !found || len(records) != 1 {
		t.Fatalf("found = %v, records = %+v, want the stub's single record", found, records)
	}
}

// TestLookupNAPTR_NoResolverConfiguredIsAnError: with nowhere to ask, the
// lookup fails. It deliberately does NOT fall back to a public resolver —
// that would tell Google or Cloudflare which organisation numbers a tenant is
// asking about.
func TestLookupNAPTR_NoResolverConfiguredIsAnError(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"an unreadable file":     filepath.Join(t.TempDir(), "does-not-exist"),
		"a file with no servers": writeResolvConf(t, "# nothing but comments\nsearch example.test\n"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resolver := &Resolver{resolvConf: path}
			_, found, err := resolver.LookupNAPTR(context.Background(), "someone.example.test")
			if err == nil {
				t.Fatal("LookupNAPTR succeeded with no DNS server configured")
			}
			if found {
				t.Error("found = true alongside an error")
			}
			if !errors.Is(err, errNoDNSServer) {
				t.Errorf("error %q is not errNoDNSServer", err)
			}
		})
	}
}

func writeResolvConf(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestNameserversFrom pins resolv.conf parsing on its own: nameserver lines
// only, IPv4 and IPv6, the default port added, everything else ignored.
func TestNameserversFrom(t *testing.T) {
	t.Parallel()
	contents := strings.Join([]string{
		"# Generated by something",
		"; an old-style comment",
		"domain example.test",
		"search example.test lan",
		"options edns0 trust-ad",
		"nameserver 192.0.2.1",
		"nameserver 2001:db8::1",
		"nameserver 192.0.2.2:5353",
		"   nameserver\t198.51.100.1   ",
		"nameserver",
		"sortlist 192.0.2.0/24",
	}, "\n")

	got, err := nameserversFrom(writeResolvConf(t, contents))
	if err != nil {
		t.Fatalf("nameserversFrom: %v", err)
	}
	want := []string{"192.0.2.1:53", "[2001:db8::1]:53", "192.0.2.2:5353", "198.51.100.1:53"}
	if len(got) != len(want) {
		t.Fatalf("nameserversFrom = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("server %d = %q, want %q", i, got[i], want[i])
		}
	}
}

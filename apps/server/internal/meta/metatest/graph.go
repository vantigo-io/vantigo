// Package metatest is a fake Meta Graph API for the meta module's tests, and
// for the integration tests that compose Communications and Meta: an
// http.RoundTripper standing in for graph.facebook.com and nothing else,
// handed to the module as Deps.HTTPTransport (modtest.WithTransport).
//
// It knows Pages (AddPage), each with its own Page access token and the tasks
// Meta lists for it, user and system-user tokens that manage Pages
// (AddUserToken), and people who wrote to a Page (AddUser). It answers
// GET /me, GET /me/accounts, POST and DELETE /{page}/subscribed_apps, POST /{page}/messages (as
// JSON and as multipart, with a filedata part) and GET /{psid}; checks the
// bearer token of every call as Meta does (an unknown or revoked token is
// Graph error 190); records every call (Calls) and every message it took
// (Sent); and fails calls on a script (FailNext, FailAll). Message ids are
// m_1, m_2, and so on. Every method is safe for concurrent use.
//
// Only _test.go files may import it (depguard's test-fakes rule).
package metatest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Host is the one host the fake answers.
const Host = "graph.facebook.com"

// Call is one request the fake received.
type Call struct {
	Method string
	// Path is the request path without the version segment: "/me",
	// "/123/messages".
	Path string
	// URL is the whole request URL, which a test asserts holds no token.
	URL string
	// Token is the bearer token of the Authorization header; "" when none.
	Token string
	// Query is the URL's query.
	Query url.Values
	// ContentType and Body are the request's, the multipart body included.
	ContentType string
	Body        []byte
	// Form is the decoded application/x-www-form-urlencoded body.
	Form url.Values
	// Status is the status the fake answered.
	Status int
}

// Sent is a message the fake took through POST /{page}/messages.
type Sent struct {
	PageID        string
	Recipient     string
	MessagingType string
	// Tag is the request's tag; "" when none.
	Tag string
	// Message is the decoded message object: text and quick_replies, or the
	// attachment descriptor of a multipart send.
	Message map[string]any
	// Text is Message["text"].
	Text string
	// FileData, FileContentType and FileName are the filedata part of a
	// multipart send; nil and "" for a JSON one.
	FileData        []byte
	FileContentType string
	FileName        string
	// MessageID is the id the fake answered.
	MessageID string
}

// Failure scripts a failed answer: a Graph error body with Status, Code,
// Subcode and Message, or, with Network set, a transport error and no answer.
type Failure struct {
	// Status is the HTTP status; 400 when zero.
	Status  int
	Code    int
	Subcode int
	Message string
	// Network makes the call fail as a dropped connection does.
	Network bool
}

type scripted struct {
	match string
	f     Failure
}

type pageRec struct {
	id, name, token string
	tasks           []string
	subscribed      string
	subscribedSet   bool
}

type managerRec struct {
	pages []string
}

type userRec struct {
	name, first, last string
}

// FakeGraph is the fake. Build it with NewFakeGraph.
type FakeGraph struct {
	mu       sync.Mutex
	pages    map[string]*pageRec
	managers map[string]*managerRec
	users    map[string]userRec
	revoked  map[string]bool
	calls    []Call
	sent     []Sent
	next     []scripted
	all      []scripted
	messages int
}

// NewFakeGraph is a fake with no Page and no person.
func NewFakeGraph() *FakeGraph {
	return &FakeGraph{pages: map[string]*pageRec{}, managers: map[string]*managerRec{}, users: map[string]userRec{}, revoked: map[string]bool{}}
}

// AddPage registers a Page with its own Page access token. tasks are what
// GET /me/accounts lists for it to a user's or a system user's token (Meta
// lists them there only): give "MESSAGING" for a Page that may be messaged.
func (g *FakeGraph) AddPage(id, name, token string, tasks ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pages[id] = &pageRec{id: id, name: name, token: token, tasks: tasks}
}

// AddUserToken registers a user's or a system user's token that manages the
// Pages (already added with AddPage): GET /me/accounts lists each with its id,
// name, tasks and its own access token. GET /me answers the user, not a Page.
func (g *FakeGraph) AddUserToken(token string, pages ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.managers[token] = &managerRec{pages: pages}
}

// AddUser registers a person who wrote to the Pages, by page-scoped id, with
// the name GET /{psid} answers.
func (g *FakeGraph) AddUser(psid, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users[psid] = userRec{name: name}
}

// AddUserParts is AddUser for a person Meta answers a first and a last name
// for and no full name.
func (g *FakeGraph) AddUserParts(psid, first, last string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users[psid] = userRec{first: first, last: last}
}

// Revoke makes every later call under token fail with Graph error 190.
func (g *FakeGraph) Revoke(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked[token] = true
}

// RenamePage changes the name GET /me answers for the Page.
func (g *FakeGraph) RenamePage(id, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p := g.pages[id]; p != nil {
		p.name = name
	}
}

// FailNext fails the next call whose "METHOD /path" (the path without the
// version) contains match, once. Scripts are used in the order given.
func (g *FakeGraph) FailNext(match string, f Failure) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next = append(g.next, scripted{match: match, f: f})
}

// FailAll fails every call whose "METHOD /path" contains match, until
// ClearFailures.
func (g *FakeGraph) FailAll(match string, f Failure) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.all = append(g.all, scripted{match: match, f: f})
}

// ClearFailures drops every scripted failure.
func (g *FakeGraph) ClearFailures() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next, g.all = nil, nil
}

// Calls is every request received, oldest first.
func (g *FakeGraph) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Call(nil), g.calls...)
}

// CallsTo is the calls whose "METHOD /path" contains match.
func (g *FakeGraph) CallsTo(match string) []Call {
	var out []Call
	for _, c := range g.Calls() {
		if strings.Contains(c.Method+" "+c.Path, match) {
			out = append(out, c)
		}
	}
	return out
}

// ResetCalls forgets the calls and the messages recorded so far.
func (g *FakeGraph) ResetCalls() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls, g.sent = nil, nil
}

// Sent is every message taken, oldest first.
func (g *FakeGraph) Sent() []Sent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Sent(nil), g.sent...)
}

// Subscribed is the subscribed_fields the Page's app subscription holds, and
// whether the Page is subscribed at all.
func (g *FakeGraph) Subscribed(pageID string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.pages[pageID]
	if p == nil {
		return "", false
	}
	return p.subscribed, p.subscribedSet
}

var versionSegment = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// RoundTrip answers a request to graph.facebook.com.
func (g *FakeGraph) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() != Host {
		return nil, fmt.Errorf("metatest: no such host %q", req.URL.Host)
	}
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	path := versionSegment.ReplaceAllString(req.URL.Path, "")
	call := Call{
		Method: req.Method, Path: path, URL: req.URL.String(), Query: req.URL.Query(),
		ContentType: req.Header.Get("Content-Type"), Body: body,
	}
	if h := req.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		call.Token = strings.TrimPrefix(h, "Bearer ")
	}
	if strings.HasPrefix(call.ContentType, "application/x-www-form-urlencoded") {
		call.Form, _ = url.ParseQuery(string(body))
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	status, payload, err := g.answer(req, call, body)
	call.Status = status
	g.calls = append(g.calls, call)
	if err != nil {
		call.Status = 0
		g.calls[len(g.calls)-1] = call
		return nil, err
	}
	b, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:  http.Header{"Content-Type": {"application/json"}},
		Body:    io.NopCloser(bytes.NewReader(b)),
		Request: req, ContentLength: int64(len(b)),
	}, nil
}

// graphErr is Meta's error body.
func graphErr(status, code, subcode int, message string) (int, any, error) {
	e := map[string]any{"message": message, "type": "OAuthException", "code": code, "fbtrace_id": "FAKE"}
	if subcode != 0 {
		e["error_subcode"] = subcode
	}
	return status, map[string]any{"error": e}, nil
}

// answer routes a call. g.mu is held.
func (g *FakeGraph) answer(req *http.Request, call Call, body []byte) (int, any, error) {
	key := call.Method + " " + call.Path
	for i, s := range g.next {
		if strings.Contains(key, s.match) {
			g.next = append(g.next[:i:i], g.next[i+1:]...)
			return failureOf(s.f)
		}
	}
	for _, s := range g.all {
		if strings.Contains(key, s.match) {
			return failureOf(s.f)
		}
	}

	page, manager := g.pageOf(call.Token), g.managerOf(call.Token)
	if page == nil && manager == nil {
		return graphErr(400, 190, 0, "Invalid OAuth access token.")
	}
	parts := strings.Split(strings.Trim(call.Path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "me" && call.Method == http.MethodGet:
		return g.me(page, call)
	case len(parts) == 2 && parts[0] == "me" && parts[1] == "accounts" && call.Method == http.MethodGet:
		return g.accounts(manager)
	case page == nil:
		// Only a Page's own token reaches a Page's edges.
		return graphErr(403, 200, 0, "A Page access token is required.")
	case len(parts) == 2 && parts[1] == "subscribed_apps":
		return g.subscription(page, parts[0], call)
	case len(parts) == 2 && parts[1] == "messages" && call.Method == http.MethodPost:
		return g.message(page, parts[0], call, body)
	case len(parts) == 1 && call.Method == http.MethodGet:
		return g.profile(parts[0], call)
	}
	return graphErr(400, 100, 33, "Unsupported request.")
}

func failureOf(f Failure) (int, any, error) {
	if f.Network {
		return 0, nil, errors.New("metatest: connection reset by peer")
	}
	status := f.Status
	if status == 0 {
		status = 400
	}
	return graphErr(status, f.Code, f.Subcode, f.Message)
}

// pageOf is the Page a Page access token reaches; nil for an unknown or
// revoked token, or a user's.
func (g *FakeGraph) pageOf(token string) *pageRec {
	if token == "" || g.revoked[token] {
		return nil
	}
	for _, p := range g.pages {
		if p.token == token {
			return p
		}
	}
	return nil
}

// managerOf is the user's or system user's token record; nil for an unknown
// or revoked token.
func (g *FakeGraph) managerOf(token string) *managerRec {
	if token == "" || g.revoked[token] {
		return nil
	}
	return g.managers[token]
}

// me answers GET /me: the Page for a Page token, the user for a user's token.
// tasks is no field of either node (Meta lists tasks on /me/accounts), so asking
// for it is error 100.
func (g *FakeGraph) me(p *pageRec, call Call) (int, any, error) {
	out := map[string]any{"id": "9000"}
	if p != nil {
		out["id"] = p.id
	}
	for _, f := range strings.Split(call.Query.Get("fields"), ",") {
		switch f {
		case "name":
			if p != nil {
				out["name"] = p.name
			} else {
				out["name"] = "System user"
			}
		case "tasks":
			node := "User"
			if p != nil {
				node = "Page"
			}
			return graphErr(400, 100, 0, "(#100) Tried accessing nonexisting field (tasks) on node type ("+node+")")
		}
	}
	return 200, out, nil
}

// accounts answers GET /me/accounts for a user's token; a Page's token has no
// such edge.
func (g *FakeGraph) accounts(m *managerRec) (int, any, error) {
	if m == nil {
		return graphErr(400, 100, 0, "(#100) Tried accessing nonexisting field (accounts) on node type (Page)")
	}
	data := []map[string]any{}
	for _, id := range m.pages {
		if pg := g.pages[id]; pg != nil {
			data = append(data, map[string]any{"id": pg.id, "name": pg.name, "tasks": pg.tasks, "access_token": pg.token})
		}
	}
	return 200, map[string]any{"data": data}, nil
}

func (g *FakeGraph) subscription(p *pageRec, id string, call Call) (int, any, error) {
	if id != p.id {
		return graphErr(403, 200, 0, "The token does not reach this Page.")
	}
	switch call.Method {
	case http.MethodPost:
		p.subscribed, p.subscribedSet = call.Form.Get("subscribed_fields"), true
	case http.MethodDelete:
		p.subscribed, p.subscribedSet = "", false
	default:
		return graphErr(400, 100, 33, "Unsupported request.")
	}
	return 200, map[string]any{"success": true}, nil
}

func (g *FakeGraph) profile(psid string, call Call) (int, any, error) {
	u, ok := g.users[psid]
	if !ok {
		return graphErr(400, 100, 33, "Unsupported get request. Object does not exist.")
	}
	out := map[string]any{"id": psid}
	for _, f := range strings.Split(call.Query.Get("fields"), ",") {
		switch f {
		case "name":
			if u.name != "" {
				out["name"] = u.name
			}
		case "first_name":
			if u.first != "" {
				out["first_name"] = u.first
			}
		case "last_name":
			if u.last != "" {
				out["last_name"] = u.last
			}
		}
	}
	return 200, out, nil
}

func (g *FakeGraph) message(p *pageRec, id string, call Call, body []byte) (int, any, error) {
	if id != p.id {
		return graphErr(403, 200, 0, "The token does not reach this Page.")
	}
	s := Sent{PageID: id}
	var recipient struct {
		ID string `json:"id"`
	}
	mediaType, params, _ := mime.ParseMediaType(call.ContentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := r.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "recipient":
				_ = json.Unmarshal(data, &recipient)
			case "message":
				_ = json.Unmarshal(data, &s.Message)
			case "messaging_type":
				s.MessagingType = string(data)
			case "tag":
				s.Tag = string(data)
			case "filedata":
				s.FileData, s.FileContentType, s.FileName = data, part.Header.Get("Content-Type"), part.FileName()
			}
		}
		if s.FileData == nil {
			return graphErr(400, 100, 0, "The filedata part is missing.")
		}
	} else {
		var in struct {
			Recipient     struct{ ID string } `json:"recipient"`
			MessagingType string              `json:"messaging_type"`
			Tag           string              `json:"tag"`
			Message       map[string]any      `json:"message"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			return graphErr(400, 100, 0, "The request is not JSON.")
		}
		recipient.ID, s.MessagingType, s.Tag, s.Message = in.Recipient.ID, in.MessagingType, in.Tag, in.Message
	}
	if recipient.ID == "" || s.Message == nil {
		return graphErr(400, 100, 0, "The recipient and the message are required.")
	}
	if s.MessagingType == "MESSAGE_TAG" && s.Tag == "" {
		return graphErr(400, 100, 0, "A tag is required for MESSAGE_TAG.")
	}
	s.Recipient = recipient.ID
	s.Text, _ = s.Message["text"].(string)
	g.messages++
	s.MessageID = fmt.Sprintf("m_%d", g.messages)
	g.sent = append(g.sent, s)
	return 200, map[string]any{"recipient_id": recipient.ID, "message_id": s.MessageID}, nil
}

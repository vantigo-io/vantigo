package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's Graph API client (Messenger design N10, N12): the
// guarded client of transport.go with Meta's calls on it. No call is retried
// here, because the outbox retries a send and a person retries an admin's
// action; a failure is answered as it is. The Page's token travels in the
// Authorization header, never in a URL; Meta's answers are read through a
// 1 MiB cap; and neither Meta's response bodies nor a message's text are
// logged — only the status and Meta's numeric code.

// maxGraphBody caps what is read of one answer.
const maxGraphBody = 1 << 20

// graphClient is the guarded client to Meta's Graph API.
type graphClient struct {
	// calls carries the JSON calls, files the uploads, under their own budgets.
	calls *http.Client
	files *http.Client
	log   *slog.Logger
}

// newGraphClient builds the client over d.
func newGraphClient(d module.Deps) *graphClient {
	return &graphClient{
		calls: newHTTPClient(d, graphOnly, callBudget),
		files: newHTTPClient(d, graphOnly, fileBudget),
		log:   loggerOf(d),
	}
}

// graphError is Meta's refusal of a call: the HTTP status and the error body's
// code, subcode and message.
type graphError struct {
	Status  int
	Code    int
	Subcode int
	Message string
}

func (e graphError) Error() string {
	if e.Subcode != 0 {
		return fmt.Sprintf("meta: the Graph API refused the call (HTTP %d, code %d/%d): %s", e.Status, e.Code, e.Subcode, e.Message)
	}
	return fmt.Sprintf("meta: the Graph API refused the call (HTTP %d, code %d): %s", e.Status, e.Code, e.Message)
}

// temporary is whether a retry may mend the refusal: Meta's own errors 1 and 2,
// the rate limits (4, 17, 32, 613 and a 429) and any 5xx.
func (e graphError) temporary() bool {
	if e.Status >= 500 || e.Status == http.StatusTooManyRequests {
		return true
	}
	switch e.Code {
	case 1, 2, 4, 17, 32, 613:
		return true
	}
	return false
}

// networkError is a call that did not reach an answer: a refused connection, a
// timeout, a body that broke off. It is always temporary.
type networkError struct{ err error }

func (e networkError) Error() string {
	return "meta: the Graph API could not be reached: " + e.err.Error()
}
func (e networkError) Unwrap() error { return e.err }

// isTemporaryGraph is whether err, from a graphClient call, is one a retry may
// mend: a network failure, or a temporary refusal.
func isTemporaryGraph(err error) bool {
	var ne networkError
	if errors.As(err, &ne) {
		return true
	}
	var ge graphError
	return errors.As(err, &ge) && ge.temporary()
}

// graphErrorBody is Meta's error body.
type graphErrorBody struct {
	Error struct {
		Message      string `json:"message"`
		Type         string `json:"type"`
		Code         int    `json:"code"`
		ErrorSubcode int    `json:"error_subcode"`
	} `json:"error"`
}

// send issues req on c and decodes a 2xx answer into out (nil: discard it).
func (g *graphClient) send(ctx context.Context, c *http.Client, req *http.Request, out any) error {
	res, err := c.Do(req.WithContext(ctx))
	if err != nil {
		g.log.WarnContext(ctx, "meta: a call to the Graph API failed", "method", req.Method)
		// A *url.Error names the URL, which holds a participant's id.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return networkError{err: err}
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxGraphBody))
	if err != nil {
		g.log.WarnContext(ctx, "meta: an answer of the Graph API broke off", "method", req.Method)
		return networkError{err: err}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		ge := graphError{Status: res.StatusCode, Message: fmt.Sprintf("HTTP %d", res.StatusCode)}
		var eb graphErrorBody
		if json.Unmarshal(body, &eb) == nil && (eb.Error.Code != 0 || eb.Error.Message != "") {
			ge.Code, ge.Subcode = eb.Error.Code, eb.Error.ErrorSubcode
			if eb.Error.Message != "" {
				ge.Message = eb.Error.Message
			}
		}
		g.log.WarnContext(ctx, "meta: the Graph API refused a call", "method", req.Method, "status", ge.Status, "code", ge.Code, "subcode", ge.Subcode)
		return ge
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("meta: read the Graph API's answer: %w", err)
	}
	return nil
}

// newRequest builds a call to path (which starts with "/") under graphBase,
// authenticated by token.
func newRequest(method, path string, query url.Values, token string, body io.Reader, contentType string) (*http.Request, error) {
	u := graphBase + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("meta: build a Graph request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// get is GET path?query, the answer decoded into out.
func (g *graphClient) get(ctx context.Context, token, path string, query url.Values, out any) error {
	req, err := newRequest(http.MethodGet, path, query, token, nil, "")
	if err != nil {
		return err
	}
	return g.send(ctx, g.calls, req, out)
}

// postJSON is POST path with in as JSON.
func (g *graphClient) postJSON(ctx context.Context, token, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("meta: encode a Graph request: %w", err)
	}
	req, err := newRequest(http.MethodPost, path, nil, token, bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}
	return g.send(ctx, g.calls, req, out)
}

// postForm is POST path with form as application/x-www-form-urlencoded.
func (g *graphClient) postForm(ctx context.Context, token, path string, form url.Values, out any) error {
	req, err := newRequest(http.MethodPost, path, nil, token, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	return g.send(ctx, g.calls, req, out)
}

// delete is DELETE path.
func (g *graphClient) delete(ctx context.Context, token, path string, out any) error {
	req, err := newRequest(http.MethodDelete, path, nil, token, nil, "")
	if err != nil {
		return err
	}
	return g.send(ctx, g.calls, req, out)
}

// upload is the multipart POST path: fields in the order given, then one file
// part named name with its bytes and content type.
func (g *graphClient) upload(ctx context.Context, token, path string, fields [][2]string, name, fileName, contentType string, data []byte, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return fmt.Errorf("meta: build a Graph upload: %w", err)
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, fileName))
	h.Set("Content-Type", contentType)
	fw, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("meta: build a Graph upload: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return fmt.Errorf("meta: build a Graph upload: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("meta: build a Graph upload: %w", err)
	}
	req, err := newRequest(http.MethodPost, path, nil, token, &buf, w.FormDataContentType())
	if err != nil {
		return err
	}
	return g.send(ctx, g.files, req, out)
}

// pagePath is "/{id}" with id escaped as one path segment.
func pagePath(id string, rest ...string) string {
	return "/" + url.PathEscape(id) + strings.Join(rest, "")
}

package google

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// This file is Google's batch endpoint for Calendar v3 (Google Workspace
// design G30): up to fifty requests sent as one multipart/mixed POST, each
// answered on its own in a multipart/mixed answer. The batch goes through the
// guarded client like any call — its host on the fixed list, its token the
// person's, its answer read through a cap — and each part's path is built
// here under /calendar/v3, never taken from an answer. A part's answer is
// its status and body; nothing of it is logged but the status and Google's
// reason.

// The batch's figures.
const (
	// calendarBatchURL is the batch endpoint of Calendar v3.
	calendarBatchURL = "https://www.googleapis.com/batch/calendar/v3"
	// calendarBatchSize is the most requests one batch holds (G30).
	calendarBatchSize = 50
	// maxBatchAnswer is the most of a batch's answer read: fifty events read
	// whole, a description each.
	maxBatchAnswer = 16 << 20
	// maxPartAnswer is the most of one part's body read.
	maxPartAnswer = 1 << 20
)

// batchPart is one request of a batch: the method, the path and query after
// the host (under /calendar/v3), and a JSON body when not nil.
type batchPart struct {
	method string
	path   string
	body   any
}

// batchReply is one part's answer: its status and its body, or err when the
// part could not be read.
type batchReply struct {
	status int
	body   []byte
}

// reason is the reply's refusal reason, "" for none.
func (r batchReply) reason() string { return reasonOf(r.body) }

// throttled reports whether the reply is a usage limit: a 429, or a 403
// naming one.
func (r batchReply) throttled() bool {
	return r.status == http.StatusTooManyRequests || (r.status == http.StatusForbidden && rateLimited(r.body))
}

// refusal is the reply as a *googleError.
func (r batchReply) refusal() *googleError { return &googleError{Status: r.status, Reason: r.reason()} }

// batch sends parts — at most calendarBatchSize — as one batch with src's
// token, quota the quotaUser of the batch itself, and answers one reply per
// part, in order. A part throttled is sent again once, alone with the others
// throttled, after serverErrorDelay; throttled again, its reply stays the
// throttle. once sends the batch once, a 5xx not retried (a batch holding a
// create). The error is the batch failing whole: Google's refusal of the
// batch, an answer that does not read, or the context ending.
func (c *googleClient) batch(ctx context.Context, src tokenSource, quota string, parts []batchPart, once bool) ([]batchReply, error) {
	if len(parts) > calendarBatchSize {
		return nil, fmt.Errorf("google: a batch of %d requests, more than %d", len(parts), calendarBatchSize)
	}
	replies := make([]batchReply, len(parts))
	send := make([]int, len(parts))
	for i := range send {
		send[i] = i
	}
	for attempt := 1; ; attempt++ {
		got, err := c.batchOnce(ctx, src, quota, parts, send, once)
		if err != nil {
			return nil, err
		}
		var again []int
		for n, i := range send {
			replies[i] = got[n]
			if got[n].throttled() && attempt == 1 {
				again = append(again, i)
			}
		}
		if len(again) == 0 {
			return replies, nil
		}
		if err := c.pause(ctx, attempt, serverErrorDelay); err != nil {
			return nil, err
		}
		send = again
	}
}

// batchOnce sends the parts at send (indices into parts) as one batch and
// answers their replies in send's order.
func (c *googleClient) batchOnce(ctx context.Context, src tokenSource, quota string, parts []batchPart, send []int, once bool) ([]batchReply, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for n, i := range send {
		p := parts[i]
		if !strings.HasPrefix(p.path, "/calendar/v3/") {
			return nil, errors.New("google: a batch part outside Calendar v3")
		}
		pw, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type": {"application/http"},
			"Content-ID":   {"<item-" + strconv.Itoa(n) + ">"},
		})
		if err != nil {
			return nil, fmt.Errorf("google: build a batch: %w", err)
		}
		_, _ = fmt.Fprintf(pw, "%s %s HTTP/1.1\r\n", p.method, p.path)
		if p.body != nil {
			data, err := json.Marshal(p.body)
			if err != nil {
				return nil, fmt.Errorf("google: encode a batch part: %w", err)
			}
			_, _ = fmt.Fprintf(pw, "Content-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(data))
			_, _ = pw.Write(data)
		} else {
			_, _ = io.WriteString(pw, "\r\n")
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("google: build a batch: %w", err)
	}
	target := calendarBatchURL
	if quota != "" {
		target += "?quotaUser=" + quota
	}
	body := &requestBody{contentType: "multipart/mixed; boundary=" + w.Boundary(), data: buf.Bytes()}
	var opts []callOption
	if once {
		opts = append(opts, noRetry())
	}
	header, data, shown, err := c.doRaw(ctx, src, http.MethodPost, target, body, maxBatchAnswer, opts...)
	if err != nil {
		return nil, err
	}
	replies, err := readBatch(header.Get("Content-Type"), data, len(send))
	if err != nil {
		return nil, fmt.Errorf("%w: POST %s: %w", errGoogleUnreachable, shown, err)
	}
	return replies, nil
}

// readBatch reads a batch's multipart/mixed answer of want parts, each matched
// to its request by its Content-ID (<response-item-N>). A part that is
// missing, or that does not read as an HTTP answer, fails the whole batch.
func readBatch(contentType string, data []byte, want int) ([]batchReply, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(media, "multipart/") || params["boundary"] == "" {
		return nil, errors.New("the batch's answer is not multipart")
	}
	replies := make([]batchReply, want)
	seen := make([]bool, want)
	r := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("the batch's answer does not read")
		}
		n, ok := partIndex(part.Header.Get("Content-ID"))
		if !ok || n >= want || seen[n] {
			return nil, errors.New("the batch's answer names a part it was not asked")
		}
		resp, err := http.ReadResponse(bufio.NewReader(part), nil)
		if err != nil {
			return nil, errors.New("a part of the batch's answer is not an HTTP answer")
		}
		body, err := readCapped(resp.Body, maxPartAnswer)
		_ = resp.Body.Close()
		if err != nil {
			return nil, errors.New("a part of the batch's answer does not read")
		}
		replies[n] = batchReply{status: resp.StatusCode, body: body}
		seen[n] = true
	}
	for _, s := range seen {
		if !s {
			return nil, errors.New("the batch's answer lacks a part")
		}
	}
	return replies, nil
}

// partIndex is the request index a part's Content-ID names: <response-item-N>.
func partIndex(id string) (int, bool) {
	id = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(id), "<"), ">")
	rest, ok := strings.CutPrefix(id, "response-item-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 0
}

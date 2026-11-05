package google

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The channel mailboxes' bounds that need no fake Gmail: a call moving a
// message under a budget scaled to it, and a delta link too long to keep.

// slowUpload is a transport that answers nothing until the request's context
// ends, recording the deadline the request carried.
type slowUpload struct {
	deadline time.Time
}

func (s *slowUpload) RoundTrip(req *http.Request) (*http.Response, error) {
	s.deadline, _ = req.Context().Deadline()
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestSizedBudget(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		n    int64
		want time.Duration
	}{
		{0, callBudget},
		{1, callBudget + time.Second},
		{3_000_000, callBudget + 3*time.Second},
		{25_000_000, callBudget + 25*time.Second},
		{1 << 40, maxCallBudget},
	} {
		if got := sizedBudget(c.n); got != c.want {
			t.Errorf("sizedBudget(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

func TestClient_ABudgetedCallOutlivesTheClientsTimeout(t *testing.T) {
	t.Parallel()
	target := gmailSendURL
	body := &requestBody{contentType: "message/rfc822", data: bytes.Repeat([]byte("a"), 1024)}

	// The upload of a large message: its deadline is the scaled budget, not
	// the http.Client's thirty seconds.
	rt := &slowUpload{}
	var log bytes.Buffer
	var waits []time.Duration
	c := testClient(rt, &log, &waits)
	c.http.Timeout = callBudget
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- c.do(ctx, &staticSource{}, http.MethodPost, target, body, nil, maxBody, noRetry(), withBudget(sizedBudget(25_000_000)))
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("the cancelled upload: %v, want context.Canceled", err)
	}
	if got := rt.deadline.Sub(start); got < callBudget+20*time.Second || got > callBudget+26*time.Second {
		t.Errorf("a 25 MB upload's deadline is %v away, want about %v", got, sizedBudget(25_000_000))
	}

	// A budget that runs out ends the upload unreachable, though the
	// http.Client's own timeout is far away.
	rt = &slowUpload{}
	c = testClient(rt, &log, &waits)
	c.http.Timeout = callBudget
	start = time.Now()
	err := c.do(context.Background(), &staticSource{}, http.MethodPost, target, body, nil, maxBody, noRetry(), withBudget(50*time.Millisecond))
	if !errors.Is(err, errGoogleUnreachable) || time.Since(start) > 10*time.Second {
		t.Errorf("a spent budget: %v after %v, want errGoogleUnreachable at once", err, time.Since(start))
	}

	// A call without one keeps the client's.
	rt = &slowUpload{}
	c = testClient(rt, &log, &waits)
	c.http.Timeout = callBudget
	ctx, cancel = context.WithCancel(context.Background())
	start = time.Now()
	go func() { done <- c.get(ctx, &staticSource{}, directoryBase+"/customers/my_customer", nil) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("the cancelled upload: %v, want context.Canceled", err)
	}
	if got := rt.deadline.Sub(start); got > callBudget+time.Second {
		t.Errorf("a plain call's deadline is %v away, want at most %v", got, callBudget)
	}
}

func TestChannelMailbox_ALinkTooLongIsADeltaExpired(t *testing.T) {
	t.Parallel()
	q := make([]string, 5000)
	for i := range q {
		q[i] = fmt.Sprintf("18c%013x", i)
	}
	_, err := (&channelMailbox{}).page(nil, mailLink{M: "123456789012345678901", H: "1", Q: q})
	if !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("a queue of %d: %v, want ErrMailboxDeltaExpired", len(q), err)
	}
	if d, err := (&channelMailbox{}).page(nil, mailLink{M: "123456789012345678901", H: "1", Q: q[:10]}); err != nil || d.Done {
		t.Errorf("a queue of ten: %+v %v", d, err)
	}
}

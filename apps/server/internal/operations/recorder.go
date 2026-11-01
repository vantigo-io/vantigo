package operations

import (
	"bytes"
	"net/http"
)

// resultMaxBytes is the most of a response Call keeps: 1 MiB.
const resultMaxBytes = 1 << 20

// recorder is the http.ResponseWriter an in-process call is served into. It
// keeps at most limit bytes of the body and remembers that it dropped the
// rest, so a handler that writes a great deal is neither stopped nor held in
// memory.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	limit  int
	total  int // every body byte the handler wrote, kept or not
	wrote  bool
}

func newRecorder(limit int) *recorder {
	return &recorder{header: http.Header{}, status: http.StatusOK, limit: limit}
}

// truncated reports whether the handler wrote more than the recorder kept.
func (r *recorder) truncated() bool { return r.total > r.limit }

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
}

func (r *recorder) Write(p []byte) (int, error) {
	r.wrote = true
	r.total += len(p)
	if room := r.limit - r.body.Len(); room > 0 {
		keep := p
		if len(keep) > room {
			keep = keep[:room]
		}
		r.body.Write(keep)
	}
	return len(p), nil
}

// Flush satisfies http.Flusher for a handler that streams; the call is not a
// stream, so there is nothing to do.
func (*recorder) Flush() {}

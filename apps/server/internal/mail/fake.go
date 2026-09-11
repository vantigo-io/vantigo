package mail

import (
	"context"
	"sync"
)

// Fake is an in-memory Sender for tests: it records every message sent and
// lets a test queue one failure for the next Send. The zero value is ready
// to use.
type Fake struct {
	mu       sync.Mutex
	messages []Message
	nextErr  error
}

// Send records m, or — if FailNext queued an error — returns that error
// instead and leaves m unrecorded.
func (f *Fake) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	f.messages = append(f.messages, m)
	return nil
}

// FailNext makes the next call to Send return err instead of recording a
// message.
func (f *Fake) FailNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextErr = err
}

// Messages returns every message recorded so far, in send order.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Message, len(f.messages))
	copy(out, f.messages)
	return out
}

// Last returns the most recently recorded message, or ok=false if Send has
// never recorded one.
func (f *Fake) Last() (m Message, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return Message{}, false
	}
	return f.messages[len(f.messages)-1], true
}

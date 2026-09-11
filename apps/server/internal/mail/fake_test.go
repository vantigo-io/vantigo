package mail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

func TestFake_RecordsMessages(t *testing.T) {
	f := &mail.Fake{}
	if _, ok := f.Last(); ok {
		t.Fatalf("Last() ok = true on an empty Fake, want false")
	}

	first := mail.Message{To: "a@example.test", Subject: "one"}
	second := mail.Message{To: "b@example.test", Subject: "two"}
	if err := f.Send(context.Background(), first); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if err := f.Send(context.Background(), second); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}

	got := f.Messages()
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("Messages() = %+v, want [%+v %+v]", got, first, second)
	}

	last, ok := f.Last()
	if !ok || last != second {
		t.Fatalf("Last() = %+v, %v, want %+v, true", last, ok, second)
	}
}

func TestFake_FailNextFailsOnlyTheNextSend(t *testing.T) {
	f := &mail.Fake{}
	boom := errors.New("boom")
	f.FailNext(boom)

	err := f.Send(context.Background(), mail.Message{To: "a@example.test"})
	if !errors.Is(err, boom) {
		t.Fatalf("Send() = %v, want %v", err, boom)
	}
	if _, ok := f.Last(); ok {
		t.Fatalf("Last() ok = true after a failed send, want false")
	}

	if err := f.Send(context.Background(), mail.Message{To: "b@example.test"}); err != nil {
		t.Fatalf("Send() = %v, want nil for the send after the failed one", err)
	}
	if last, ok := f.Last(); !ok || last.To != "b@example.test" {
		t.Fatalf("Last() = %+v, %v, want the second message", last, ok)
	}
}

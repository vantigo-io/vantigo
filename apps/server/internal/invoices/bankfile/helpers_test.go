package bankfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
)

func day(d int) time.Time { return time.Date(2026, time.October, d, 0, 0, 0, 0, time.UTC) }

// today is the Oslo day every test parses on: the fixtures settle on 5 and
// 6 October 2026.
var today = day(6)

const (
	accountA = "12345678903"
	accountB = "86011117947"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := readFixture(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readFixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("testdata", "ocr", name))
}

func mustParse(t *testing.T, b []byte) *bankfile.File {
	t.Helper()
	f, err := bankfile.ParseOCR(b, today)
	if err != nil {
		t.Fatalf("ParseOCR: %v", err)
	}
	return f
}

// records splits a built file into its records; join puts them back.
func records(b []byte) []string {
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func join(recs []string) []byte { return []byte(strings.Join(recs, "\n") + "\n") }

// put writes s over rec from the 1-based position pos, as the format's
// tables number them.
func put(rec string, pos int, s string) string {
	return rec[:pos-1] + s + rec[pos-1+len(s):]
}

// refusal asserts err is a *bankfile.Error at where whose message holds
// every one of want.
func refusal(t *testing.T, name string, err error, where string, want ...string) {
	t.Helper()
	var e *bankfile.Error
	if !errors.As(err, &e) {
		t.Errorf("%s: err = %v, want a *bankfile.Error at %s", name, err, where)
		return
	}
	if e.Where != where {
		t.Errorf("%s: refused at %q (%s), want %q", name, e.Where, e.Message, where)
	}
	for _, w := range want {
		if !strings.Contains(e.Message, w) {
			t.Errorf("%s: message %q does not say %q", name, e.Message, w)
		}
	}
}

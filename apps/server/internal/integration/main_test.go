package integration_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestMain installs a tracer provider for the whole suite and, once every
// test has run, fails the suite if any span it exported held an email
// address: in its name, an attribute, an event, a link or its status (the
// attribute policy, Kubernetes readiness KR24). The suite composes the real
// modules over the real database and drives them through the HTTP stack, so
// its server spans, query spans, worker cycles and outbound calls are the
// ones production exports.
func TestMain(m *testing.M) {
	leaks := &emailLeaks{seen: map[string]string{}}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(leaks))
	otel.SetTracerProvider(tp)
	code := m.Run()
	_ = tp.Shutdown(context.Background())
	if found := leaks.report(); len(found) > 0 {
		fmt.Fprintln(os.Stderr, "FAIL: exported span attributes hold email addresses (the attribute policy, contributing/platform-packages):")
		for _, f := range found {
			fmt.Fprintln(os.Stderr, "  "+f)
		}
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// emailShaped is an email address anywhere in a value.
var emailShaped = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

// emailLeaks is a span processor that remembers, per span name and key, the
// first email-shaped value it saw, so the suite's memory does not grow with
// the spans it makes.
type emailLeaks struct {
	mu   sync.Mutex
	seen map[string]string
}

func (e *emailLeaks) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (e *emailLeaks) Shutdown(context.Context) error                  { return nil }
func (e *emailLeaks) ForceFlush(context.Context) error                { return nil }

func (e *emailLeaks) OnEnd(s sdktrace.ReadOnlySpan) {
	check := func(where string, v string) {
		if m := emailShaped.FindString(v); m != "" {
			e.mu.Lock()
			key := s.Name() + " " + where
			if _, ok := e.seen[key]; !ok {
				e.seen[key] = m
			}
			e.mu.Unlock()
		}
	}
	checkAttrs := func(where string, kvs []attribute.KeyValue) {
		for _, kv := range kvs {
			check(where+string(kv.Key), kv.Value.String())
		}
	}
	check("name", s.Name())
	check("status", s.Status().Description)
	checkAttrs("", s.Attributes())
	for _, ev := range s.Events() {
		check("event "+ev.Name, ev.Name)
		checkAttrs("event "+ev.Name+" ", ev.Attributes)
	}
	for _, l := range s.Links() {
		checkAttrs("link ", l.Attributes)
	}
}

func (e *emailLeaks) report() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.seen))
	for k, v := range e.seen {
		out = append(out, fmt.Sprintf("span %s: %q", k, v))
	}
	sort.Strings(out)
	return out
}

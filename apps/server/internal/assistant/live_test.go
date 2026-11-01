package assistant_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestLive_ARealLocalModelAnswersAHowTo runs the assistant against a real
// OpenAI-compatible endpoint, such as a local Ollama, instead of the fake. It
// is skipped unless ASSISTANT_LIVE_AI_URL (such as http://localhost:11434/v1)
// and ASSISTANT_LIVE_AI_MODEL are set:
//
//	ASSISTANT_LIVE_AI_URL=http://localhost:11434/v1 ASSISTANT_LIVE_AI_MODEL=gemma4:31b \
//	  go test ./internal/assistant -run TestLive -v
func TestLive_ARealLocalModelAnswersAHowTo(t *testing.T) {
	url, model := os.Getenv("ASSISTANT_LIVE_AI_URL"), os.Getenv("ASSISTANT_LIVE_AI_MODEL")
	if url == "" || model == "" {
		t.Skip("ASSISTANT_LIVE_AI_URL and ASSISTANT_LIVE_AI_MODEL are not set")
	}
	h := newHarness(t, nil,
		modtest.WithEnv("AI_BASE_URL", url),
		modtest.WithEnv("AI_MODELS", model),
		modtest.WithEnv("AI_TIMEOUT", "300s"),
	)
	c, _ := person(t, h)
	conv := startConversation(t, c)
	// A real model is slower than the test client's timeout, and the run is
	// detached from the connection: send, hang up, and follow it by polling.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ask(t, c, conv.ID, "How do I add a metering point?", modtest.Context(ctx), modtest.SkipContract("a live run, followed by polling"))
	waitForLong(t, 8*time.Minute, func() bool { return !running(t, c, conv.ID) })
	d := getConversation(t, c, conv.ID)
	for _, m := range d.Messages {
		for _, p := range m.Parts {
			t.Logf("%s/%s: %.400s %.200s", m.Role, p.Type, p.Text, p.Content)
		}
	}
	t.Logf("actions: %+v", d.Actions)
}

func waitForLong(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("timed out")
}

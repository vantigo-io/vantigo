package ai

import (
	"errors"
	"fmt"
)

// The AI call's failure modes, as distinct types. A consumer records the
// error's type name — never its message, which could carry provider or
// customer text — as a short, non-identifying label (communications writes it
// to ai_interactions.error_summary through `%T`), so the names below are part
// of what an audit table holds and are kept as they were when the client
// lived in Communications. Every error Complete returns is one of these, so
// that label is always meaningful: a bare *url.Error would reduce to the
// useless name "Error".
type (
	// aiTransportError is a request that never produced a response: a dial
	// failure, a timeout, a cancelled context, or an unreadable body. It keeps
	// the underlying cause so a caller can tell a cancellation apart from a
	// genuine provider failure with errors.Is — communications' callerGaveUp
	// must not record a vanished caller as an outage. The cause is never
	// rendered into a message or a label.
	aiTransportError struct{ err error }
	// aiStatusError is a response with a non-2xx status.
	aiStatusError struct{ status int }
	// aiDecodeError is a 2xx response whose body is not the shape the
	// provider's API documents.
	aiDecodeError struct{}
	// aiEmptyResponseError is a well-formed response carrying no choice, so
	// there is no completion text to draft or parse from.
	aiEmptyResponseError struct{}
	// aiRefusalError is a well-formed answer the provider's safeguards declined
	// to give (Anthropic's refusal stop reason): nothing usable came back, and
	// asking again unchanged declines again. It carries the policy category the
	// provider named, "" when it named none.
	aiRefusalError struct{ category string }
	// aiMaxTokensError is an answer the provider cut off at the token limit,
	// thinking included. Half an answer, and above all half a tool call, is
	// never handed on as whole.
	aiMaxTokensError struct{}
	// aiModelUnavailableError is the Resolver failing to say which model the
	// task uses (its table could not be read): nothing was sent.
	aiModelUnavailableError struct{ err error }
	// aiModelNotAllowedError is the Resolver naming a model AI_MODELS does not
	// allow: nothing was sent. A well-behaved Resolver never causes it; it is
	// the guard that makes the allowed list true whoever implements the seam.
	aiModelNotAllowedError struct{}
)

func (aiTransportError) Error() string { return "ai: the AI provider could not be reached" }

// Unwrap exposes the cause to errors.Is only; nothing ever formats it into a
// persisted or returned string.
func (e aiTransportError) Unwrap() error { return e.err }

func (e aiStatusError) Error() string {
	return fmt.Sprintf("ai: the AI provider responded %d", e.status)
}

func (aiDecodeError) Error() string { return "ai: the AI provider's response could not be decoded" }

func (aiEmptyResponseError) Error() string { return "ai: the AI provider returned no completion" }

func (aiRefusalError) Error() string { return "ai: the AI provider declined to answer" }

func (aiMaxTokensError) Error() string { return "ai: the AI provider's answer reached the token limit" }

// RefusalCategory reports whether err is the provider declining to answer, and
// the policy category it named ("cyber", "bio", ...), empty when it named none.
func RefusalCategory(err error) (category string, refused bool) {
	var r aiRefusalError
	if errors.As(err, &r) {
		return r.category, true
	}
	return "", false
}

// IsMaxTokens reports whether err is an answer cut off at the token limit.
func IsMaxTokens(err error) bool {
	var m aiMaxTokensError
	return errors.As(err, &m)
}

func (aiModelUnavailableError) Error() string {
	return "ai: the model for the task could not be resolved"
}

func (e aiModelUnavailableError) Unwrap() error { return e.err }

func (aiModelNotAllowedError) Error() string {
	return "ai: the model for the task is not one the plan allows"
}

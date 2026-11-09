package assistant

import (
	"encoding/json"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
)

// The module's refusal codes, stable: the interface keys off them, never the
// words.
const (
	codeUnavailable   = "assistant_unavailable"
	codeNotFound      = "not_found"
	codeRunInProgress = "run_in_progress"
	codeVersion       = "version_conflict"
	codeInvalid       = "invalid"
	codeAIUnavailable = "ai_unavailable"
	// codeAgentUnavailable: a conversation with a virtual employee cannot go on
	// (V31): the agent was paused or deleted, or the person lost agents:view.
	codeAgentUnavailable = "agent_unavailable"
)

// refusal is a problem+json answer. It satisfies every operation's response
// interface (the Visit methods below), so a handler returns it for any
// refusal its contract documents without a type per operation and status.
type refusal struct {
	status int
	code   string
	title  string
	detail string
	errors map[string][]string
}

// refuse builds a refusal.
func refuse(status int, code, title string) refusal {
	return refusal{status: status, code: code, title: title}
}

// invalid is a 400 with field errors.
func invalid(title string, errs map[string][]string) refusal {
	return refusal{status: http.StatusBadRequest, code: codeInvalid, title: title, errors: errs}
}

func notFound() refusal {
	return refuse(http.StatusNotFound, codeNotFound, "Not found")
}

func unavailable() refusal {
	return refuse(http.StatusForbidden, codeUnavailable, "The assistant is not available to you")
}

func (r refusal) Error() string { return r.code + ": " + r.title }

func (r refusal) write(w http.ResponseWriter) error {
	status, code, title := int32(r.status), r.code, r.title
	problem := gen.AssistantProblem{Status: &status, Code: &code, Title: &title}
	if r.detail != "" {
		problem.Detail = &r.detail
	}
	if len(r.errors) > 0 {
		problem.Errors = &r.errors
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(r.status)
	return json.NewEncoder(w).Encode(problem)
}

func (r refusal) VisitCancelAssistantPendingActionResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitConfirmAssistantPendingActionResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitListAssistantAgentsResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitGetAssistantUsageResponse(w http.ResponseWriter) error   { return r.write(w) }
func (r refusal) VisitGetAssistantBulkJobResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitContinueAssistantBulkJobResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitStopAssistantBulkJobResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetAssistantBulkJobFailuresResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitCreateAssistantConversationResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitDeleteAssistantConversationResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetAssistantAvailabilityResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetAssistantConversationResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetAssistantMeResponse(w http.ResponseWriter) error          { return r.write(w) }
func (r refusal) VisitGetAssistantPermissionsResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitGetAssistantPersonResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r refusal) VisitGetAssistantSettingsResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r refusal) VisitListAssistantConversationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitPutAssistantMeResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r refusal) VisitPutAssistantPermissionResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitPutAssistantPersonResponse(w http.ResponseWriter) error     { return r.write(w) }
func (r refusal) VisitPutAssistantSettingsResponse(w http.ResponseWriter) error   { return r.write(w) }
func (r refusal) VisitSendAssistantMessageResponse(w http.ResponseWriter) error   { return r.write(w) }
func (r refusal) VisitStopAssistantRunResponse(w http.ResponseWriter) error       { return r.write(w) }
func (r refusal) VisitUpdateAssistantConversationResponse(w http.ResponseWriter) error {
	return r.write(w)
}

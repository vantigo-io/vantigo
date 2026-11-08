package agents

import (
	"encoding/json"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
)

// The module's refusal codes, stable: the interface keys off them, never the
// words.
const (
	codeNotFound        = "not_found"
	codeInvalid         = "invalid"
	codeVersion         = "version_conflict"
	codeCompanyName     = "company_name_required"
	codeChannelBound    = "channel_bound"
	codeOwnerRequired   = "owner_required"
	codeLevelLocked     = "level_locked"
	codeAIUnavailable   = "ai_unavailable"
	statusOwnerRequired = http.StatusForbidden
)

// refusal is a problem+json answer. It satisfies every operation's response
// interface (the Visit methods below), so a handler returns it for any
// refusal its contract documents without a type per operation and status.
type refusal struct {
	status int
	code   string
	title  string
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

func notFound() refusal { return refuse(http.StatusNotFound, codeNotFound, "Not found") }

func versionConflict() refusal {
	return refuse(http.StatusConflict, codeVersion, "It changed since you read it")
}

func ownerRequired() refusal {
	return refuse(statusOwnerRequired, codeOwnerRequired, "Only the Owner may do this")
}

func badIfMatch() refusal {
	return invalid("If-Match is not a version", map[string][]string{"If-Match": {"If-Match must carry the version the row was read at, such as \"3\""}})
}

func (r refusal) Error() string { return r.code + ": " + r.title }

func (r refusal) write(w http.ResponseWriter) error {
	status, code, title := int32(r.status), r.code, r.title
	problem := gen.AgentsProblem{Status: &status, Code: &code, Title: &title}
	if len(r.errors) > 0 {
		problem.Errors = &r.errors
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(r.status)
	return json.NewEncoder(w).Encode(problem)
}

func (r refusal) VisitListAgentsResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r refusal) VisitCreateAgentResponse(w http.ResponseWriter) error        { return r.write(w) }
func (r refusal) VisitDeleteAgentResponse(w http.ResponseWriter) error        { return r.write(w) }
func (r refusal) VisitGetAgentResponse(w http.ResponseWriter) error           { return r.write(w) }
func (r refusal) VisitUpdateAgentResponse(w http.ResponseWriter) error        { return r.write(w) }
func (r refusal) VisitGetAgentAvatarResponse(w http.ResponseWriter) error     { return r.write(w) }
func (r refusal) VisitPutAgentAvatarResponse(w http.ResponseWriter) error     { return r.write(w) }
func (r refusal) VisitPauseAgentResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r refusal) VisitResumeAgentResponse(w http.ResponseWriter) error        { return r.write(w) }
func (r refusal) VisitPutAgentPermissionResponse(w http.ResponseWriter) error { return r.write(w) }
func (r refusal) VisitCreateAgentRoleResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r refusal) VisitDeleteAgentRoleResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r refusal) VisitUpdateAgentRoleResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r refusal) VisitSetAgentRoleChannelsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
func (r refusal) VisitGetAgentSetupResponse(w http.ResponseWriter) error      { return r.write(w) }
func (r refusal) VisitGetAgentsSettingsResponse(w http.ResponseWriter) error  { return r.write(w) }
func (r refusal) VisitPutAgentsSettingsResponse(w http.ResponseWriter) error  { return r.write(w) }
func (r refusal) VisitListAgentTemplatesResponse(w http.ResponseWriter) error { return r.write(w) }

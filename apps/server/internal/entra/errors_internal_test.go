package entra

import (
	"net/http"
	"testing"
)

// TestProblems pins each refusal's status and code: the codes are what a
// client keys off (EntraProblem in entra.yaml), so a builder answering the
// wrong one is a contract break, not a wording change.
func TestProblems(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int32
		code   *string
		want   int
		wantC  string
	}{
		{"connectionInUse", *connectionInUse(3).Status, connectionInUse(3).Code, http.StatusConflict, codeConnectionInUse},
		{"credentialInUse", *credentialInUse().Status, credentialInUse().Code, http.StatusConflict, codeCredentialInUse},
		{"versionMismatch", *versionMismatch(4, 3).Status, versionMismatch(4, 3).Code, http.StatusPreconditionFailed, codeVersionMismatch},
		{"credentialInvalid", *tenantUnavailable(codeCredentialInvalid).Status, tenantUnavailable(codeCredentialInvalid).Code, http.StatusServiceUnavailable, codeCredentialInvalid},
		{"tenantUnreachable", *tenantUnavailable(codeTenantUnreachable).Status, tenantUnavailable(codeTenantUnreachable).Code, http.StatusServiceUnavailable, codeTenantUnreachable},
	} {
		if int(c.status) != c.want || c.code == nil || *c.code != c.wantC {
			t.Errorf("%s = %d %v, want %d %s", c.name, c.status, c.code, c.want, c.wantC)
		}
	}

	var f fieldErrors
	f.add("tenantId", "")
	if f.failed() {
		t.Error("an empty message recorded a failure")
	}
	f.add("tenantId", "The tenant id is a GUID")
	p := invalid("Invalid connection", f)
	if !f.failed() || *p.Status != http.StatusBadRequest || p.Code != nil || len((*p.Errors)["tenantId"]) != 1 {
		t.Errorf("invalid = %+v, want a 400 with the one field error and no code", p)
	}
}

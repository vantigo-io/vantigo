package gen

import (
	"net/http"
	"testing"
)

// The generated strict server must mount on a std-lib mux; this fails to
// compile if the generator's output or its common.yaml import mapping breaks.
func TestGeneratedServerMountsOnServeMux(t *testing.T) {
	var server StrictServerInterface // nil: only the types are under test
	handler := HandlerFromMux(NewStrictHandler(server, nil), http.NewServeMux())
	if handler == nil {
		t.Fatal("HandlerFromMux returned nil")
	}
}

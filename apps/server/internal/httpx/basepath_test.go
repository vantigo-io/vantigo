package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripBasePath(t *testing.T) {
	tests := []struct{ base, path, want string }{
		{"", "/api/x", "/api/x"},
		{"/crm", "/crm", "/"},
		{"/crm", "/crm/", "/"},
		{"/crm", "/crm/api/v1/x", "/api/v1/x"},
		{"/crm", "/crmx/api", "/crmx/api"},
		{"/crm", "/health/ready", "/health/ready"},
		{"/erp/crm", "/erp/crm/assets/a.js", "/assets/a.js"},
	}
	for _, tc := range tests {
		t.Run(tc.base+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			var got string
			StripBasePath(tc.base)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
			})).ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
			if req.URL.Path != tc.path {
				t.Errorf("the caller's request was mutated to %q", req.URL.Path)
			}
		})
	}
}

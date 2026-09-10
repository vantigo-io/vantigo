package security

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAllowedHosts_AddsLoopbackOnce(t *testing.T) {
	if got := AllowedHosts("vantigo.example.com"); !slices.Equal(got, []string{"vantigo.example.com", "localhost", "127.0.0.1", "::1"}) {
		t.Errorf("AllowedHosts = %v", got)
	}
	if got := AllowedHosts("localhost"); !slices.Equal(got, []string{"localhost", "127.0.0.1", "::1"}) {
		t.Errorf("AllowedHosts(localhost) = %v", got)
	}
}

func TestHostFilter(t *testing.T) {
	filter := HostFilter(AllowedHosts("vantigo.example.com"))
	tests := []struct {
		host string
		want int
	}{
		{"vantigo.example.com", http.StatusOK},
		{"VANTIGO.example.com:443", http.StatusOK},
		{"localhost:8080", http.StatusOK},
		{"127.0.0.1:8080", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"evil.example", http.StatusBadRequest},
		{"vantigo.example.com.evil.example", http.StatusBadRequest},
		{"", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			filter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("Host %q: status %d, want %d", tc.host, rec.Code, tc.want)
			}
			if tc.want == http.StatusBadRequest && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("rejection is not a problem document: %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

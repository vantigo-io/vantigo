package entra

import (
	"slices"
	"strings"
	"testing"
)

// returnTo is held to a path on this installation (Outlook connection
// design M4, plan reading 15): anything else becomes the Connections page.
func TestSafeReturnTo(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"", outlookPagePath},
		{"/calendar/settings", "/calendar/settings"},
		{"/settings/connections?tab=1", "/settings/connections?tab=1"},
		{"/%5Cx", "/%5Cx"},
		{"//x", outlookPagePath},
		{"/\\x", outlookPagePath},
		{"/a\\b", outlookPagePath},
		{"https://x", outlookPagePath},
		{"javascript:x", outlookPagePath},
		{"calendar", outlookPagePath},
		{"/a\nb", outlookPagePath},
		{"/a\tb", outlookPagePath},
		{"/a\x7f", outlookPagePath},
		{"/" + strings.Repeat("a", 511), "/" + strings.Repeat("a", 511)},
		{"/" + strings.Repeat("a", 512), outlookPagePath},
	} {
		if got := safeReturnTo(tc.in); got != tc.want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The granted scopes are kept lower-cased, sorted and compacted, Graph's
// resource prefix dropped (plan reading 12.6).
func TestNormaliseScopes(t *testing.T) {
	got := normaliseScopes("https://graph.microsoft.com/Calendars.ReadWrite User.Read openid  profile calendars.readwrite HTTPS://GRAPH.MICROSOFT.COM/User.Read")
	if want := []string{"calendars.readwrite", "openid", "profile", "user.read"}; !slices.Equal(got, want) {
		t.Errorf("normaliseScopes = %v, want %v", got, want)
	}
	if got := normaliseScopes(""); len(got) != 0 {
		t.Errorf("normaliseScopes(\"\") = %v, want none", got)
	}
	if !hasScopes([]string{"calendars.readwrite", "openid"}, outlookFeatures["calendar"]) {
		t.Error("the calendar feature not found in its scope")
	}
	if hasScopes([]string{"openid", "user.read"}, outlookFeatures["calendar"]) {
		t.Error("the calendar feature found without its scope")
	}
	if got := featuresOf([]string{"calendars.readwrite", "openid"}); !slices.Equal(got, []string{"calendar"}) {
		t.Errorf("featuresOf = %v", got)
	}
}

// A connected cookie a browser could drop silently is refused (plan
// reading 7).
func TestConnectedCookieFits(t *testing.T) {
	if !connectedCookieFits(strings.Repeat("a", maxConnectedCookie)) {
		t.Errorf("a cookie of %d bytes refused", maxConnectedCookie)
	}
	if connectedCookieFits(strings.Repeat("a", maxConnectedCookie+1)) {
		t.Errorf("a cookie of %d bytes admitted", maxConnectedCookie+1)
	}
}

// Microsoft's refusals of a code are read by code alone (plan reading
// 12.4).
func TestRedeemRefusalCode(t *testing.T) {
	for _, tc := range []struct {
		error string
		codes []int
		want  string
	}{
		{"invalid_grant", []int{70008}, codeExpired},
		{"invalid_grant", []int{54005}, codeExpired},
		{"invalid_grant", []int{501481}, outlookFailed},
		{"invalid_client", []int{7000215}, outlookFailed},
		{"server_error", nil, outlookFailed},
	} {
		if got := redeemRefusalCode(tc.error, tc.codes); got != tc.want {
			t.Errorf("%s %v = %s, want %s", tc.error, tc.codes, got, tc.want)
		}
	}
}

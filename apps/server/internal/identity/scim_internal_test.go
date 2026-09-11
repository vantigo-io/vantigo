package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// scimErrorOf is err's SCIM answer as "status scimType: detail", or "" for
// no error.
func scimErrorOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var e scimError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a SCIM answer", err)
	}
	return fmt.Sprintf("%d %s: %s", e.status, e.scimType, e.detail)
}

// New: .NET's filter grammar (SV/SCIM:1059-1079), its regex exactly,
// including the keep-the-first-and-last-clause quirk and .NET's \s.
func TestScimFilter(t *testing.T) {
	const pattern = `400 invalidFilter: Only field eq "value" filters joined by and are supported.`
	const field = "400 invalidFilter: The filter field or value is invalid."
	padding := strings.Repeat("a", 512-len(`userName eq ""`))
	longest := `userName eq "` + padding + `"`
	cases := []struct {
		name, raw string
		allowed   []string
		want      string // the clauses as attribute=value, or the refusal
	}{
		{"no filter", "", nil, ""},
		{"blank", " \t ", nil, ""},
		{"one clause", `userName eq "bob"`, nil, "userName=bob"},
		{"any case, canonical attribute", `USERNAME EQ "bob"`, nil, "userName=bob"},
		{"two clauses", `externalId eq "x" and userName eq "y"`, nil, "externalId=x userName=y"},
		{"one attribute twice", `userName eq "a" AND userName eq "b"`, nil, "userName=a userName=b"},
		{"three clauses keep the first and last", `userName eq "a" and nickName eq "ignored" and externalId eq "c"`, nil, "userName=a externalId=c"},
		{"four clauses keep the first and last", `userName eq "a" and x eq "1" and y eq "2" and externalId eq "d"`, nil, "userName=a externalId=d"},
		{"surrounding space", `  userName eq "a"  `, nil, "userName=a"},
		{"JSON escapes", `userName eq "a\"bé"`, nil, "userName=a\"bé"},
		{".NET's \\s: a no-break space", "userName\u00a0eq\u00a0\"x\"", nil, "userName=x"},
		{".NET's \\s: a vertical tab", "userName\veq\v\"x\"", nil, "userName=x"},
		{"at most 512 characters", longest, nil, "userName=" + padding},
		{"513 characters", longest + " ", nil, "400 invalidFilter: The filter is too long."},
		{"a Groups attribute on Users", `displayName eq "x"`, nil, field},
		{"a Users attribute on Groups", `userName eq "x"`, []string{"displayName", "externalId"}, field},
		{"a Groups filter", `displayName eq "Staff"`, []string{"displayName", "externalId"}, "displayName=Staff"},
		{"a disallowed last clause", `userName eq "a" and title eq "b"`, nil, field},
		{"not eq", `userName ne "x"`, nil, pattern},
		{"unquoted", `userName eq x`, nil, pattern},
		{"or", `userName eq "x" or externalId eq "y"`, nil, pattern},
		{"dangling and", `userName eq "x" and`, nil, pattern},
		{"digit first", `1userName eq "x"`, nil, pattern},
		{"no space", `userName eq"x"`, nil, pattern},
		{"a bad escape", `userName eq "\x"`, nil, field},
		{"a raw control character", "userName eq \"a\tb\"", nil, field},
	}
	for _, c := range cases {
		allowed := c.allowed
		if allowed == nil {
			allowed = []string{"userName", "externalId"}
		}
		clauses, err := scimFilter(c.raw, allowed...)
		got := scimErrorOf(t, err)
		if err == nil {
			var parts []string
			for _, cl := range clauses {
				parts = append(parts, cl.attribute+"="+cl.value)
			}
			got = strings.Join(parts, " ")
		}
		if got != c.want {
			t.Errorf("%s: %q gives %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}

// New: paging as .NET read it (SV/SCIM:1049-1057, :1165): each parameter
// through two parsers, so a signed or padded integer counts as the default.
func TestScimPaging(t *testing.T) {
	const refused = "400 invalidValue: startIndex and count are invalid."
	cases := []struct {
		query string
		want  string // "startIndex count", or the refusal
	}{
		{"", "1 100"},
		{"startIndex=2&count=5", "2 5"},
		{"count=0", "1 0"},
		{"count=100", "1 100"},
		{"count=101", refused},
		{"startIndex=0", refused},
		{"startIndex=-1", "1 100"},
		{"startIndex=%2B3", "1 100"},
		{"count=-5", "1 100"},
		{"startIndex=+5", "1 100"},
		{"startIndex=%205", "1 100"},
		{"startIndex=5%20", "1 100"},
		{"startIndex=007&count=007", "7 7"},
		{"STARTINDEX=3&Count=4", "3 4"},
		{"startIndex=2147483647", "2147483647 100"},
		{"startIndex=2147483648", refused},
		{"startIndex=-2147483648", "1 100"},
		{"startIndex=-2147483649", refused},
		{"startIndex=abc", refused},
		{"startIndex=", refused},
		{"startIndex=5.0", refused},
		{"startIndex=1&startIndex=2", refused},
		{"count=1&COUNT=2", refused},
		{"filter=x&startIndex=3", "3 100"},
	}
	for _, c := range cases {
		start, count, err := scimPaging(c.query)
		got := scimErrorOf(t, err)
		if err == nil {
			got = fmt.Sprintf("%d %d", start, count)
		}
		if got != c.want {
			t.Errorf("%q gives %q, want %q", c.query, got, c.want)
		}
	}
}

// describeUserActions is actions as field=value, field- for a removal, and
// active=true|false.
func describeUserActions(actions []scimUserAction) string {
	var out []string
	for _, a := range actions {
		switch {
		case a.remove:
			out = append(out, a.field+"-")
		case a.active != nil:
			out = append(out, fmt.Sprintf("%s=%t", a.field, *a.active))
		case a.value != nil:
			out = append(out, a.field+"="+*a.value)
		default:
			out = append(out, a.field+"?")
		}
	}
	return strings.Join(out, " ")
}

// patchBody is a PatchOp with the operations, each a JSON object.
func patchBody(ops ...string) string {
	return `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[` + strings.Join(ops, ",") + `]}`
}

// New: every User PATCH path .NET handled (SV/SCIM:757-912), Entra's
// pathless objects included, and each of its refusals.
func TestScimUserPatch(t *testing.T) {
	const unsupported = "400 invalidPath: The PatchOp path is not supported."
	const notString = "400 invalidValue: The PatchOp value must be a string."
	const noOperations = "400 invalidSyntax: PatchOp Operations must be a non-empty array."
	const malformed = "400 invalidSyntax: Each PatchOp operation must contain an op and a value or path."
	const nameParts = "400 invalidPath: Only name.givenName and name.familyName are supported."
	cases := []struct {
		name, body, want string
	}{
		{"not an object", `[]`, noOperations},
		{"another schema", `{"schemas":["urn:x"],"Operations":[{"op":"add","path":"active","value":true}]}`, "400 invalidValue: The resource schema is not supported."},
		{"schemas not strings", `{"schemas":[1],"Operations":[{"op":"add","path":"active","value":true}]}`, "400 invalidValue: schemas must be an array of strings."},
		{"no schemas", `{"Operations":[{"op":"add","path":"active","value":true}]}`, "active=true"},
		{"no Operations", `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"]}`, noOperations},
		{"empty Operations", patchBody(), noOperations},
		{"Operations not an array", `{"Operations":{"op":"add"}}`, noOperations},
		{"Operations in any case", `{"operations":[{"op":"add","path":"active","value":true}]}`, "active=true"},
		{"no op", patchBody(`{"path":"active","value":true}`), malformed},
		{"op not a string", patchBody(`{"op":1,"path":"active","value":true}`), malformed},
		{"neither value nor path", patchBody(`{"op":"add"}`), malformed},
		{"an operation not an object", patchBody(`"add"`), malformed},
		{"another op", patchBody(`{"op":"move","path":"active","value":true}`), "400 invalidSyntax: Only Add, Replace, and Remove are supported."},
		{"op trimmed, any case", patchBody(`{"op":" Replace ","path":"active","value":false}`), "active=false"},
		{"active as a string", patchBody(`{"op":"replace","path":"active","value":"False"}`), "400 invalidValue: active must be boolean."},
		{"remove active", patchBody(`{"op":"remove","path":"active"}`), "active-"},
		{"userName, untrimmed", patchBody(`{"op":"replace","path":"userName","value":" bob "}`), "userName= bob "},
		{"path in any case", patchBody(`{"op":"replace","path":"USERNAME","value":"bob"}`), "userName=bob"},
		{"path trimmed", patchBody(`{"op":"replace","path":" userName ","value":"bob"}`), "userName=bob"},
		{"remove userName", patchBody(`{"op":"remove","path":"userName"}`), "400 mutability: userName cannot be removed."},
		{"remove externalId", patchBody(`{"op":"remove","path":"externalId"}`), "400 mutability: externalId cannot be removed."},
		{"blank userName", patchBody(`{"op":"replace","path":"userName","value":" "}`), "400 invalidValue: userName is invalid."},
		{"userName with a control character", patchBody(`{"op":"replace","path":"userName","value":"a\u0007"}`), "400 invalidValue: userName is invalid."},
		{"userName not a string", patchBody(`{"op":"replace","path":"userName","value":5}`), notString},
		{"externalId", patchBody(`{"op":"replace","path":"externalId","value":"ext-1"}`), "externalId=ext-1"},
		{"blank externalId", patchBody(`{"op":"add","path":"externalId","value":"  "}`), "400 invalidValue: externalId is invalid."},
		{"displayName", patchBody(`{"op":"add","path":"displayName","value":"Ann"}`), "displayName=Ann"},
		{"displayName over 200", patchBody(`{"op":"add","path":"displayName","value":"` + strings.Repeat("x", 201) + `"}`), "400 invalidValue: A name value is invalid."},
		{"remove displayName", patchBody(`{"op":"remove","path":"displayName"}`), "displayName-"},
		{"filtered work email", patchBody(`{"op":"replace","path":"emails[type eq \"work\"].value","value":"a@b.example"}`), "email=a@b.example"},
		{"email path in any case", patchBody(`{"op":"replace","path":"EMAILS.VALUE","value":"a@b.example"}`), "email=a@b.example"},
		{"an invalid email", patchBody(`{"op":"replace","path":"emails.value","value":"not an email"}`), "400 invalidValue: The work email is invalid."},
		{"remove the email", patchBody(`{"op":"remove","path":"emails.value"}`), "email-"},
		{"emails without .value", patchBody(`{"op":"replace","path":"emails","value":"a@b.example"}`), unsupported},
		{"name.givenName", patchBody(`{"op":"replace","path":"name.givenName","value":"Ann"}`), "givenName=Ann"},
		{"name.familyName in any case", patchBody(`{"op":"replace","path":"Name.FamilyName","value":"Lee"}`), "familyName=Lee"},
		{"name with one part", patchBody(`{"op":"replace","path":"name","value":{"familyName":"Lee"}}`), "familyName=Lee"},
		{"name with two parts", patchBody(`{"op":"replace","path":"name","value":{"givenName":"A","familyName":"B"}}`), nameParts},
		{"name with another part", patchBody(`{"op":"replace","path":"name","value":{"middleName":"A"}}`), nameParts},
		{"name part names are case-exact", patchBody(`{"op":"replace","path":"name","value":{"GivenName":"A"}}`), nameParts},
		{"name not an object", patchBody(`{"op":"replace","path":"name","value":"A"}`), "400 invalidValue: name must be an object."},
		{"remove name.givenName", patchBody(`{"op":"remove","path":"name.givenName"}`), "givenName-"},
		{"another path", patchBody(`{"op":"replace","path":"title","value":"x"}`), unsupported},
		{"a path is required", patchBody(`{"op":"replace","value":"x"}`), "400 invalidPath: A path is required for this PatchOp value."},
		{"a blank path is none", patchBody(`{"op":"replace","path":" ","value":"x"}`), "400 invalidPath: A path is required for this PatchOp value."},
		{"Entra's pathless object", patchBody(`{"op":"Replace","value":{"active":false,"displayName":"New","name":{"givenName":"G","familyName":"F"},"emails":[{"type":"work","value":" n@example.test ","primary":true}]}}`),
			"active=false displayName=New givenName=G familyName=F email=n@example.test"},
		{"pathless keys in any case", patchBody(`{"op":"replace","value":{"USERNAME":"u","Name":{"givenName":"G"}}}`), "userName=u givenName=G"},
		{"a pathless path-named key", patchBody(`{"op":"replace","value":{"name.familyName":"F"}}`), "familyName=F"},
		{"a pathless name with another part", patchBody(`{"op":"replace","value":{"name":{"middleName":"M"}}}`), nameParts},
		{"a pathless null name", patchBody(`{"op":"replace","value":{"name":null}}`), "400 invalidValue: name must be an object."},
		{"pathless emails without a work email", patchBody(`{"op":"replace","value":{"emails":[{"type":"home","value":"h@example.test"}]}}`), notString},
		{"a pathless removal of the emails", patchBody(`{"op":"remove","value":{"emails":[]}}`), "email-"},
		{"pathless emails not an array", patchBody(`{"op":"replace","value":{"emails":{}}}`), "400 invalidValue: emails must be an array."},
		{"pathless two work emails", patchBody(`{"op":"replace","value":{"emails":[{"type":"work","value":"a@x.test"},{"type":"WORK","value":"b@x.test"}]}}`), "400 invalidValue: Only one primary work email is supported."},
		{"a pathless extension attribute", patchBody(`{"op":"replace","value":{"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User":{"department":"x"}}}`), unsupported},
		{"the first refusal wins", patchBody(`{"op":"replace","path":"active","value":true}`, `{"op":"replace","path":"title","value":"x"}`), unsupported},
		{"several operations in order", patchBody(`{"op":"replace","path":"active","value":true}`, `{"op":"replace","path":"displayName","value":"D"}`), "active=true displayName=D"},
	}
	for _, c := range cases {
		ops, err := readScimPatch([]byte(c.body))
		var actions []scimUserAction
		if err == nil {
			actions, err = scimUserActions(ops)
		}
		got := scimErrorOf(t, err)
		if err == nil {
			got = describeUserActions(actions)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// describeGroupActions is actions as displayName=…, active=…, members+[…]
// (present), members-[…] (absent), members=[…] (replace) or members*
// (every member absent).
func describeGroupActions(actions []scimGroupAction) string {
	var out []string
	for _, a := range actions {
		switch {
		case a.field == "displayName":
			out = append(out, "displayName="+a.name)
		case a.field == "active":
			out = append(out, fmt.Sprintf("active=%t", a.active))
		case a.removeAll:
			out = append(out, "members*")
		case a.replace:
			out = append(out, fmt.Sprintf("members=%v", a.memberIDs))
		case a.present:
			out = append(out, fmt.Sprintf("members+%v", a.memberIDs))
		default:
			out = append(out, fmt.Sprintf("members-%v", a.memberIDs))
		}
	}
	return strings.Join(out, " ")
}

// New: every Group PATCH path .NET handled (SV/SCIM:830-873), including its
// pathless remove, which makes every member absent.
func TestScimGroupPatch(t *testing.T) {
	const unsupported = "400 invalidPath: The PatchOp path is not supported."
	cases := []struct {
		name, body, want string
	}{
		{"active", patchBody(`{"op":"replace","path":"active","value":false}`), "active=false"},
		{"active not a boolean", patchBody(`{"op":"replace","path":"active","value":"false"}`), unsupported},
		{"remove active", patchBody(`{"op":"remove","path":"active"}`), unsupported},
		{"displayName", patchBody(`{"op":"replace","path":"displayName","value":"New"}`), "displayName=New"},
		{"a blank displayName", patchBody(`{"op":"replace","path":"displayName","value":" "}`), unsupported},
		{"remove displayName", patchBody(`{"op":"remove","path":"displayName"}`), unsupported},
		{"add members", patchBody(`{"op":"add","path":"members","value":[{"value":"a"},{"value":"b"}]}`), "members+[a b]"},
		{"members as strings", patchBody(`{"op":"add","path":"members","value":["a"]}`), "members+[a]"},
		{"replace members", patchBody(`{"op":"replace","path":"members","value":[{"value":"a"}]}`), "members=[a]"},
		{"remove every member", patchBody(`{"op":"remove","path":"members"}`), "members*"},
		{"remove one member", patchBody(`{"op":"remove","path":"members[value eq \"a\"]"}`), "members-[a]"},
		{"a member filter in any case", patchBody(`{"op":"Remove","path":"MEMBERS[VALUE EQ \"a\"]"}`), "members-[a]"},
		{"a member filter with .NET's \\s", patchBody(`{"op":"remove","path":"members[value\u00a0eq\u00a0\"a\"]"}`), "members-[a]"},
		{"add under a member filter", patchBody(`{"op":"add","path":"members[value eq \"a\"]","value":[{"value":"b"}]}`), "members+[b]"},
		{"another member filter", patchBody(`{"op":"remove","path":"members[type eq \"User\"]"}`), unsupported},
		{"members not an array", patchBody(`{"op":"add","path":"members","value":{"value":"a"}}`), "400 invalidValue: members PatchOp values must be an array."},
		{"a blank member", patchBody(`{"op":"add","path":"members","value":[{"value":""}]}`), "400 invalidValue: A group member id is required."},
		{"a member not a string", patchBody(`{"op":"add","path":"members","value":[5]}`), "400 invalidValue: A group member id is required."},
		{"pathless members", patchBody(`{"op":"add","value":{"members":[{"value":"a"}]}}`), "members+[a]"},
		{"a pathless remove makes every member absent", patchBody(`{"op":"remove","value":{"members":[{"value":"a"}]}}`), "members*"},
		{"pathless displayName and active", patchBody(`{"op":"replace","value":{"displayName":"X","active":true}}`), "displayName=X active=true"},
		{"a pathless blank displayName", patchBody(`{"op":"replace","value":{"displayName":""}}`), unsupported},
		{"a pathless externalId", patchBody(`{"op":"replace","value":{"externalId":"x"}}`), unsupported},
		{"an empty path is a path", patchBody(`{"op":"replace","path":"","value":{"active":true}}`), unsupported},
		{"another op", patchBody(`{"op":"move","path":"active","value":true}`), "400 invalidSyntax: Only Add, Replace, and Remove are supported."},
	}
	for _, c := range cases {
		ops, err := readScimPatch([]byte(c.body))
		var actions []scimGroupAction
		if err == nil {
			actions, err = scimGroupActions(ops)
		}
		got := scimErrorOf(t, err)
		if err == nil {
			got = describeGroupActions(actions)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// New: a User and a Group resource read as .NET's JsonElement reading read
// them (SV/SCIM:698-755): a mistyped field is refused with .NET's detail,
// or ignored where .NET ignored it.
func TestScimReadResources(t *testing.T) {
	str := func(p *string) string {
		if p == nil {
			return "-"
		}
		return *p
	}
	user := func(body string) string {
		in, err := readScimUser([]byte(body))
		if err != nil {
			return scimErrorOf(t, err)
		}
		active := "-"
		if in.active != nil {
			active = fmt.Sprint(*in.active)
		}
		return strings.Join([]string{str(in.userName), str(in.externalID), str(in.displayName), active,
			str(in.profile.givenName), str(in.profile.familyName), str(in.profile.email)}, "|")
	}
	userCases := []struct{ body, want string }{
		{`[]`, "400 invalidSyntax: A SCIM resource must be a JSON object."},
		{`null`, "400 invalidSyntax: A SCIM resource must be a JSON object."},
		{`{"userName":" bob ","externalId":" {6F9619FF-8B86-D011-B42D-00CF4FC964FF} ","displayName":" Bob ","active":false}`,
			"bob|6f9619ff-8b86-d011-b42d-00cf4fc964ff|Bob|false|-|-|-"},
		{`{"USERNAME":"bob","ExternalId":" ext "}`, "bob|ext|-|-|-|-|-"},
		{`{"userName":5}`, "400 invalidValue: userName is required."},
		{`{"userName":" "}`, "400 invalidValue: userName is required."},
		{`{"userName":"bob","active":"yes"}`, "400 invalidValue: active must be boolean."},
		{`{"userName":"bob","active":null}`, "400 invalidValue: active must be boolean."},
		{`{"userName":"bob","name":"x"}`, "400 invalidValue: name must be an object."},
		{`{"userName":"bob","name":{"givenName":" A ","familyName":5}}`, "bob|-|-|-|A|-|-"},
		{`{"userName":"bob","emails":{}}`, "400 invalidValue: emails must be an array."},
		{`{"userName":"bob","emails":[{"type":"work","value":"a@x.test"},{"type":"work","value":"b@x.test","primary":true}]}`, "400 invalidValue: Only one primary work email is supported."},
		{`{"userName":"bob","emails":[{"type":"Work","value":" a@x.test "}]}`, "bob|-|-|-|-|-|a@x.test"},
		{`{"userName":"bob","emails":[{"type":5,"value":"a@x.test"},{"type":"work","primary":"true","value":"b@x.test"},"c@x.test"]}`, "bob|-|-|-|-|-|-"},
		{`{"userName":"bob","emails":[{"type":"work","primary":false,"value":"a@x.test"},{"type":"work","value":"b@x.test"}]}`, "bob|-|-|-|-|-|b@x.test"},
		{`{"userName":"bob","displayName":7,"externalId":7}`, "bob|-|-|-|-|-|-"},
		{`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"userName":"bob"}`, "400 invalidValue: The resource schema is not supported."},
		{`{"userName":"a","userName":"b"}`, "a|-|-|-|-|-|-"},
	}
	for _, c := range userCases {
		if got := user(c.body); got != c.want {
			t.Errorf("user %s: got %q, want %q", c.body, got, c.want)
		}
	}

	group := func(body string) string {
		in, err := readScimGroup([]byte(body))
		if err != nil {
			return scimErrorOf(t, err)
		}
		return fmt.Sprintf("%s|%s|%t|%v", in.displayName, str(in.externalID), in.active, in.memberIDs)
	}
	groupCases := []struct{ body, want string }{
		{`"x"`, "400 invalidSyntax: A SCIM resource must be a JSON object."},
		{`{"displayName":" Staff ","externalId":" e ","members":[{"value":"a"},{"value":"b","type":"user"},{"value":"c","type":5}]}`, "Staff| e |true|[a b c]"},
		{`{"displayName":" "}`, "400 invalidValue: displayName is required."},
		{`{"displayName":"S","active":"no"}`, "400 invalidValue: active must be boolean."},
		{`{"displayName":"S","active":false}`, "S|-|false|[]"},
		{`{"displayName":"S","members":{}}`, "400 invalidValue: members must be an array."},
		{`{"displayName":"S","members":[{"value":"a","type":"Group"}]}`, "400 invalidValue: Only User members are supported."},
		{`{"displayName":"S","members":[{"value":5}]}`, "400 invalidValue: Only User members are supported."},
		{`{"displayName":"S","members":["a"]}`, "400 invalidValue: Only User members are supported."},
		{`{"displayName":"S","externalId":""}`, "400 invalidValue: externalId is invalid."},
		{`{"displayName":"S","externalId":5}`, "S|-|true|[]"},
	}
	for _, c := range groupCases {
		if got := group(c.body); got != c.want {
			t.Errorf("group %s: got %q, want %q", c.body, got, c.want)
		}
	}
}

// New: HttpRequest.Query as .NET read it: keys in any case, values decoded
// and joined with a comma.
func TestScimQueryValue(t *testing.T) {
	cases := []struct {
		query, name, want string
		present           bool
	}{
		{"", "filter", "", false},
		{"filter=a", "filter", "a", true},
		{"FILTER=a&Filter=b", "filter", "a,b", true},
		{"filter=userName+eq+%22a%22", "filter", `userName eq "a"`, true},
		{"filter", "filter", "", true},
		{"filter=%zz+x", "filter", "%zz x", true},
		{"other=1", "filter", "", false},
	}
	for _, c := range cases {
		if got, present := queryValue(c.query, c.name); got != c.want || present != c.present {
			t.Errorf("%q[%s] = %q %t, want %q %t", c.query, c.name, got, present, c.want, c.present)
		}
	}
	if !scimMembersExcluded("excludedAttributes=displayName,%20Members") || scimMembersExcluded("excludedAttributes=member") {
		t.Error("excludedAttributes is not read as .NET's IsMembersExcluded")
	}
}

// New: the bearer token as .NET read it (SV/SCIM:541-545), and the current
// and previous tokens as ScimTokenService checked them.
func TestScimBearerToken(t *testing.T) {
	cases := []struct {
		headers []string
		want    string // the token, or "-" for none
	}{
		{nil, "-"},
		{[]string{"Bearer abc"}, "abc"},
		{[]string{"bearer abc"}, "abc"},
		{[]string{"BEARER   abc  "}, "abc"},
		{[]string{"Bearer "}, "-"},
		{[]string{"Bearer a b"}, "-"},
		{[]string{"Bearerabc"}, "-"},
		{[]string{"Basic abc"}, "-"},
		{[]string{"Bearer abc", "Bearer def"}, "-"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, h := range c.headers {
			r.Header.Add("Authorization", h)
		}
		got, ok := scimBearer(r)
		if !ok {
			got = "-"
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.headers, got, c.want)
		}
	}

	expires := time.Date(2026, time.September, 11, 13, 0, 0, 0, time.UTC)
	a := &Access{cfg: &config.Config{SCIM: &config.SCIMConfig{Token: "current", PreviousToken: "previous", PreviousTokenExpiresAt: expires}}}
	checks := []struct {
		token string
		at    time.Time
		valid bool
	}{
		{"current", expires.Add(time.Hour), true},
		{"previous", expires.Add(-time.Second), true},
		{"previous", expires, false},
		{"current ", expires, false},
		{"curren", expires, false},
		{"", expires, false},
	}
	for _, c := range checks {
		if got := a.scimTokenValid(c.token, c.at); got != c.valid {
			t.Errorf("%q at %s: valid %t, want %t", c.token, c.at, got, c.valid)
		}
	}
	if (&Access{cfg: &config.Config{}}).scimTokenValid("current", expires) {
		t.Error("a token is valid while SCIM is not configured")
	}
}

// New: JsonDocument's limits (EA/SCIM:59): UTF-8, one document, nested at
// most 64 deep, a bracket in a string not counting.
func TestValidScimJSON(t *testing.T) {
	nested := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	cases := []struct {
		body string
		want bool
	}{
		{`{}`, true},
		{``, false},
		{`{} {}`, false},
		{`{"a":`, false},
		{nested(64), true},
		{nested(65), false},
		{`{"a":"` + strings.Repeat("[", 100) + `"}`, true},
		{`{"a":"\"` + strings.Repeat("{", 100) + `"}`, true},
		{"{\"a\":\"\xff\"}", false},
	}
	for _, c := range cases {
		if got := validScimJSON([]byte(c.body)); got != c.want {
			t.Errorf("%.40q: %t, want %t", c.body, got, c.want)
		}
	}
}

// New: If-Match as CheckPrecondition read it (SV/SCIM:1171-1177), and a
// PatchOp's meta.version as CheckMetaVersion did (:1178-1185).
func TestScimPrecondition(t *testing.T) {
	cases := []struct {
		ifMatch string // "<none>" for no header
		stale   bool
	}{
		{"<none>", false},
		{"", false},
		{"*", false},
		{`"e1"`, false},
		{`e1`, false},
		{`"x", "e1"`, false},
		{` "e1" `, false},
		{`"x"`, true},
		{`W/"e1"`, true},
		{`"x",*`, true},
	}
	for _, c := range cases {
		var ifMatch *string
		if c.ifMatch != "<none>" {
			ifMatch = ptr(c.ifMatch)
		}
		if got := scimPrecondition(ifMatch, "e1") != nil; got != c.stale {
			t.Errorf("If-Match %q: stale %t, want %t", c.ifMatch, got, c.stale)
		}
	}
	if !scimBodyVersionStale([]byte(`{"META":{"Version":"x"}}`), "e1") || scimBodyVersionStale([]byte(`{"meta":{"version":5}}`), "e1") ||
		scimBodyVersionStale([]byte(`{"meta":{"version":"e1"}}`), "e1") || scimBodyVersionStale([]byte(`{}`), "e1") {
		t.Error("meta.version is not read as CheckMetaVersion read it")
	}
}

// New: OrdinalIgnoreCase maps each character to its simple uppercase, so
// the Kelvin sign is not a k, where strings.EqualFold's folding says it is.
func TestEqualOrdinalIgnoreCase(t *testing.T) {
	if !equalOrdinalIgnoreCase("userName", "USERNAME") || equalOrdinalIgnoreCase("userName", "userNam") ||
		equalOrdinalIgnoreCase("k", "\u212a") || !strings.EqualFold("k", "\u212a") {
		t.Error("equalOrdinalIgnoreCase is not .NET's OrdinalIgnoreCase")
	}
}

// New: the SCIM ingress limit is .NET's ScimIngressRateLimiter
// (EA/ScimProtocolEndpoints.cs:94-103).
func TestScimIngressPolicy(t *testing.T) {
	if p := policyScimIngress; p.Limit != 120 || p.Window != time.Minute || p.NoRetryAfter || p.Name == "" {
		t.Errorf("policy = %+v", p)
	}
}

// New: the discovery documents are .NET's (SV/SCIM:51-109), property for
// property.
func TestScimDiscoveryDocuments(t *testing.T) {
	s := &server{}
	s.deps.Config = &config.Config{}
	config, err := s.GetIdentityScimV2ServiceProviderConfig(t.Context(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(config)
	want := `{"authenticationSchemes":[{"description":"Deployment-bound bearer token issued for one SCIM connection.","documentationUri":"https://www.rfc-editor.org/rfc/rfc7644","name":"SCIM bearer token","primary":true,"specUri":"https://www.rfc-editor.org/rfc/rfc6750","type":"oauthbearertoken"}],` +
		`"bulk":{"maxOperations":0,"maxPayloadSize":0,"supported":false},"changePassword":{"supported":false},"documentationUri":"https://www.rfc-editor.org/rfc/rfc7644",` +
		`"etag":{"supported":true},"filter":{"maxResults":100,"supported":true},"patch":{"supported":true},"schemas":["urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"],"sort":{"supported":false}}`
	if string(b) != want {
		t.Errorf("ServiceProviderConfig\n got %s\nwant %s", b, want)
	}

	schemas, err := s.GetIdentityScimV2Schemas(t.Context(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(schemas)
	// attr is one attribute as it marshals, its properties in the generated
	// struct's order.
	attr := func(caseExact bool, multi bool, mutability, name string, required bool, sub, typ, uniqueness string) string {
		out := "{"
		if caseExact {
			out += `"caseExact":true,`
		}
		out += fmt.Sprintf(`"multiValued":%t,"mutability":%q,"name":%q,"required":%t,"returned":"default",`, multi, mutability, name, required)
		if sub != "" {
			out += `"subAttributes":[` + sub + `],`
		}
		out += fmt.Sprintf(`"type":%q`, typ)
		if uniqueness != "" {
			out += `,"uniqueness":"server"`
		}
		return out + "}"
	}
	rw := "readWrite"
	user := `{"attributes":[` + strings.Join([]string{
		attr(true, false, rw, "userName", true, "", "string", "server"),
		attr(true, false, rw, "externalId", true, "", "string", "server"),
		attr(false, false, rw, "active", false, "", "boolean", ""),
		attr(true, false, rw, "displayName", false, "", "string", ""),
		attr(false, false, rw, "name", false, attr(false, false, rw, "givenName", false, "", "string", "")+","+attr(false, false, rw, "familyName", false, "", "string", ""), "complex", ""),
		attr(false, true, rw, "emails", false, attr(false, false, rw, "value", false, "", "string", "")+","+attr(false, false, rw, "type", false, "", "string", "")+","+attr(false, false, rw, "primary", false, "", "boolean", ""), "complex", ""),
	}, ",") + `],"description":"SCIM User","id":"urn:ietf:params:scim:schemas:core:2.0:User","name":"User"}`
	group := `{"attributes":[` + strings.Join([]string{
		attr(true, false, rw, "externalId", false, "", "string", "server"),
		attr(true, false, rw, "displayName", true, "", "string", "server"),
		attr(false, false, rw, "active", false, "", "boolean", ""),
		attr(false, true, rw, "members", false, attr(false, false, rw, "value", true, "", "string", "")+","+attr(false, false, "readOnly", "$ref", false, "", "reference", "")+","+attr(false, false, rw, "type", false, "", "string", ""), "complex", ""),
	}, ",") + `],"description":"SCIM Group","id":"urn:ietf:params:scim:schemas:core:2.0:Group","name":"Group"}`
	want = `{"resources":[` + user + "," + group + `],"schemas":["urn:ietf:params:scim:api:messages:2.0:ListResponse"],"totalResults":2}`
	if string(b) != want {
		t.Errorf("Schemas\n got %s\nwant %s", b, want)
	}

	types, err := s.GetIdentityScimV2ResourceTypes(t.Context(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(types)
	want = `{"resources":[{"description":"SCIM User","endpoint":"/api/v1/identity/scim/v2/Users","id":"User","name":"User","schema":"urn:ietf:params:scim:schemas:core:2.0:User"},` +
		`{"description":"SCIM Group","endpoint":"/api/v1/identity/scim/v2/Groups","id":"Group","name":"Group","schema":"urn:ietf:params:scim:schemas:core:2.0:Group"}],` +
		`"schemas":["urn:ietf:params:scim:schemas:core:2.0:ResourceType"],"totalResults":2}`
	if string(b) != want {
		t.Errorf("ResourceTypes\n got %s\nwant %s", b, want)
	}
}

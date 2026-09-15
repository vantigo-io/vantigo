package identity

import (
	"github.com/vantigo-io/vantigo/server/internal/apicommon"

	"bytes"
	"encoding/json"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// The reading .NET's ScimProtocolService did itself ("SV/SCIM"). It read
// the query through HttpRequest.Query and the body as a JsonElement, field
// by field, and answered each malformed input with its own scimType and
// detail. These functions port that reading, so a mistyped field is refused,
// or ignored, exactly as .NET refused or ignored it.

// jsonKind is a JSON value's kind, as JsonElement.ValueKind names them;
// jsonUndefined is a value that is not there (default(JsonElement)).
type jsonKind int

const (
	jsonUndefined jsonKind = iota
	jsonObject
	jsonArray
	jsonString
	jsonNumber
	jsonTrue
	jsonFalse
	jsonNull
)

// jsonKindOf is v's kind. v is a value of a document the ingress validated
// (validScimJSON), or nil.
func jsonKindOf(v json.RawMessage) jsonKind {
	b := bytes.TrimLeft(v, " \t\r\n")
	if len(b) == 0 {
		return jsonUndefined
	}
	switch b[0] {
	case '{':
		return jsonObject
	case '[':
		return jsonArray
	case '"':
		return jsonString
	case 't':
		return jsonTrue
	case 'f':
		return jsonFalse
	case 'n':
		return jsonNull
	default:
		return jsonNumber
	}
}

// jsonMember is one property of a JSON object.
type jsonMember struct {
	name  string
	value json.RawMessage
}

// jsonMembers is object v's properties in document order, duplicates
// included, as JsonElement.EnumerateObject yields them; nil for any other
// kind.
func jsonMembers(v json.RawMessage) []jsonMember {
	if jsonKindOf(v) != jsonObject {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	var out []jsonMember
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return out
		}
		name, _ := key.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return out
		}
		out = append(out, jsonMember{name: name, value: value})
	}
	return out
}

// jsonElements is array v's items; nil for any other kind.
func jsonElements(v json.RawMessage) []json.RawMessage {
	if jsonKindOf(v) != jsonArray {
		return nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(v, &out); err != nil {
		return nil
	}
	return out
}

// jsonProperty is .NET's TryGet (SV/SCIM:1146-1151): the first property of
// object v whose name equals name, ignoring case ordinally.
func jsonProperty(v json.RawMessage, name string) (json.RawMessage, bool) {
	for _, m := range jsonMembers(v) {
		if equalOrdinalIgnoreCase(m.name, name) {
			return m.value, true
		}
	}
	return nil, false
}

// jsonStringOf is TryGetStringValue (:1133-1137): v's text when v is a
// string.
func jsonStringOf(v json.RawMessage) (string, bool) {
	if jsonKindOf(v) != jsonString {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsonReadString is ReadString (:1153): object v's property name when it is
// a string, else nil.
func jsonReadString(v json.RawMessage, name string) *string {
	p, ok := jsonProperty(v, name)
	if !ok {
		return nil
	}
	s, ok := jsonStringOf(p)
	if !ok {
		return nil
	}
	return &s
}

// jsonBool is ParseBoolean (:1139-1144): v's value when v is true or false.
func jsonBool(v json.RawMessage) (value, ok bool) {
	switch jsonKindOf(v) {
	case jsonTrue:
		return true, true
	case jsonFalse:
		return false, true
	default:
		return false, false
	}
}

// jsonNesting is how deeply body's objects and arrays nest. body is valid
// JSON, so a bracket outside a string opens or closes one.
func jsonNesting(body []byte) int {
	depth, deepest := 0, 0
	inString, escaped := false, false
	for _, c := range body {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			depth++
			deepest = max(deepest, depth)
		case c == '}' || c == ']':
			depth--
		}
	}
	return deepest
}

// scimMaxJSONDepth is JsonDocument's default MaxDepth, the nesting .NET
// parsed a body to (EA/SCIM:59).
const scimMaxJSONDepth = 64

// validScimJSON reports whether body is one JSON document .NET's
// JsonDocument.ParseAsync accepted: UTF-8, valid JSON, and nested at most
// scimMaxJSONDepth deep.
func validScimJSON(body []byte) bool {
	return utf8.Valid(body) && json.Valid(body) && jsonNesting(body) <= scimMaxJSONDepth
}

// equalOrdinalIgnoreCase is .NET's StringComparison.OrdinalIgnoreCase as
// every caller here uses it: to compare a name with an ASCII one (a
// property, a path, a query key, "work", "User"). There .NET equates no
// other character with an ASCII letter: it refuses uſerName and memberſ,
// whose U+017F Unicode folds to s. So only ASCII letters fold, and every
// other byte must match exactly.
func equalOrdinalIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if asciiLower(a[i]) != asciiLower(b[i]) {
			return false
		}
	}
	return true
}

func asciiLower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// dotnetTrim is String.Trim(): leading and trailing white space
// (char.IsWhiteSpace) removed.
func dotnetTrim(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

// dotnetBlank is String.IsNullOrWhiteSpace for a present string.
func dotnetBlank(s string) bool {
	return dotnetTrim(s) == ""
}

// trimmed is `value?.Trim()`.
func trimmed(v *string) *string {
	if v == nil {
		return nil
	}
	s := dotnetTrim(*v)
	return &s
}

// isSafeValue is IsSafeValue (SV/SCIM:1154): present, not blank, at most
// limit characters (UTF-16, as .NET counted) and no control character.
func isSafeValue(v *string, limit int) bool {
	return v != nil && !dotnetBlank(*v) && utf16Length(*v) <= limit && !strings.ContainsFunc(*v, unicode.IsControl)
}

// normalizeExternalID is NormalizeExternalId (:1155): a GUID in any of the
// forms .NET parses becomes its lower-case dashed form; anything else is
// trimmed.
func normalizeExternalID(v *string) *string {
	if v == nil {
		return nil
	}
	if id, ok := parseGUID(*v); ok {
		s := id.String()
		return &s
	}
	return trimmed(v)
}

// validMailAddress stands in for new MailAddress(value), which .NET
// required a work email to parse as (:685-689, :821-824); net/mail stands
// in for it as it does for the OIDC claim policy's.
func validMailAddress(v string) bool {
	_, err := mail.ParseAddress(v)
	return err == nil
}

// scimUserID is a User's id as a path names it. .NET compared ids as
// strings (:241-242), so only the lower-case dashed form an id is issued
// in names a User.
func scimUserID(id string) (uuid.UUID, bool) {
	u, err := uuid.Parse(id)
	return u, err == nil && u.String() == id
}

// readSchemas is TryReadSchemas (:1081-1090): a resource may leave schemas
// out, as older Entra payloads did; when present they must be strings, and
// one of them expected.
func readSchemas(root json.RawMessage, expected string) error {
	schemas, ok := jsonProperty(root, "schemas")
	if !ok {
		return nil
	}
	if jsonKindOf(schemas) != jsonArray {
		return scimInvalidValue("schemas must be an array of strings.")
	}
	named := false
	for _, item := range jsonElements(schemas) {
		s, ok := jsonStringOf(item)
		if !ok {
			return scimInvalidValue("schemas must be an array of strings.")
		}
		named = named || s == expected
	}
	if !named {
		return scimInvalidValue("The resource schema is not supported.")
	}
	return nil
}

// scimProfile is ScimProfile (:1211): the name parts and the work email a
// User resource carried, trimmed.
type scimProfile struct {
	givenName, familyName, email *string
}

// readProfile is ReadProfileFromJson (:1092-1111).
func readProfile(root json.RawMessage) (scimProfile, error) {
	var p scimProfile
	if name, ok := jsonProperty(root, "name"); ok {
		if jsonKindOf(name) != jsonObject {
			return p, scimInvalidValue("name must be an object.")
		}
		p.givenName, p.familyName = trimmed(jsonReadString(name, "givenName")), trimmed(jsonReadString(name, "familyName"))
	}
	if emails, ok := jsonProperty(root, "emails"); ok {
		email, err := readWorkEmail(emails)
		if err != nil {
			return p, err
		}
		p.email = email
	}
	return p, nil
}

// readWorkEmail is ReadProfileFromJson's emails (:1101-1109): of the
// objects whose type is work (in any case) and that are primary or do not
// say, at most one, whose value is the email.
func readWorkEmail(emails json.RawMessage) (*string, error) {
	if jsonKindOf(emails) != jsonArray {
		return nil, scimInvalidValue("emails must be an array.")
	}
	var candidates []json.RawMessage
	for _, item := range jsonElements(emails) {
		if jsonKindOf(item) != jsonObject {
			continue
		}
		if typ := jsonReadString(item, "type"); typ == nil || !equalOrdinalIgnoreCase(*typ, "work") {
			continue
		}
		if primary, ok := jsonProperty(item, "primary"); ok && jsonKindOf(primary) != jsonTrue {
			continue
		}
		candidates = append(candidates, item)
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return trimmed(jsonReadString(candidates[0], "value")), nil
	default:
		return nil, scimInvalidValue("Only one primary work email is supported.")
	}
}

// scimUserInput is ScimUserInput (:1212): a User resource as POST and PUT
// read it.
type scimUserInput struct {
	userName, externalID, displayName *string
	active                            *bool
	profile                           scimProfile
}

// readScimUser is TryReadUser (:698-724), with userName required, as both
// its callers required it.
func readScimUser(body []byte) (scimUserInput, error) {
	var in scimUserInput
	root := json.RawMessage(body)
	if jsonKindOf(root) != jsonObject {
		return in, scimInvalidSyntax("A SCIM resource must be a JSON object.")
	}
	if err := readSchemas(root, scimUserSchema); err != nil {
		return in, err
	}
	userName := jsonReadString(root, "userName")
	if userName == nil || dotnetBlank(*userName) {
		return in, scimInvalidValue("userName is required.")
	}
	if active, ok := jsonProperty(root, "active"); ok {
		b, ok := jsonBool(active)
		if !ok {
			return in, scimInvalidValue("active must be boolean.")
		}
		in.active = &b
	}
	profile, err := readProfile(root)
	if err != nil {
		return in, err
	}
	in.userName = trimmed(userName)
	in.externalID = normalizeExternalID(jsonReadString(root, "externalId"))
	in.displayName = trimmed(jsonReadString(root, "displayName"))
	in.profile = profile
	return in, nil
}

// scimGroupInput is ScimGroupInput (:1213).
type scimGroupInput struct {
	displayName string
	externalID  *string
	active      bool
	memberIDs   []string
}

// readScimGroup is TryReadGroup (:726-755), with displayName required, as
// its caller required it. A group's externalId is neither trimmed nor
// normalized, as .NET left it.
func readScimGroup(body []byte) (scimGroupInput, error) {
	var in scimGroupInput
	root := json.RawMessage(body)
	if jsonKindOf(root) != jsonObject {
		return in, scimInvalidSyntax("A SCIM resource must be a JSON object.")
	}
	if err := readSchemas(root, scimGroupSchema); err != nil {
		return in, err
	}
	displayName := trimmed(jsonReadString(root, "displayName"))
	if displayName == nil || dotnetBlank(*displayName) {
		return in, scimInvalidValue("displayName is required.")
	}
	in.displayName, in.active = *displayName, true
	if active, ok := jsonProperty(root, "active"); ok {
		b, ok := jsonBool(active)
		if !ok {
			return in, scimInvalidValue("active must be boolean.")
		}
		in.active = b
	}
	if members, ok := jsonProperty(root, "members"); ok {
		if jsonKindOf(members) != jsonArray {
			return in, scimInvalidValue("members must be an array.")
		}
		for _, member := range jsonElements(members) {
			id, typ := jsonReadString(member, "value"), jsonReadString(member, "type")
			if id == nil || dotnetBlank(*id) || typ != nil && !dotnetBlank(*typ) && !equalOrdinalIgnoreCase(*typ, "User") {
				return in, scimInvalidValue("Only User members are supported.")
			}
			in.memberIDs = append(in.memberIDs, *id)
		}
	}
	in.externalID = jsonReadString(root, "externalId")
	if in.externalID != nil && !isSafeValue(in.externalID, 512) {
		return in, scimInvalidValue("externalId is invalid.")
	}
	return in, nil
}

// scimPatchOp is ScimPatchOperation (:1214): value is nil when the
// operation has none.
type scimPatchOp struct {
	op    string
	path  *string
	value json.RawMessage
}

// readScimPatch is TryReadPatch (:757-772). A body that is not an object
// is refused as one without Operations: .NET returned no result for it,
// which ASP.NET answered with a 500.
func readScimPatch(body []byte) ([]scimPatchOp, error) {
	root := json.RawMessage(body)
	if jsonKindOf(root) != jsonObject {
		return nil, scimInvalidSyntax("PatchOp Operations must be a non-empty array.")
	}
	if err := readSchemas(root, scimPatchSchema); err != nil {
		return nil, err
	}
	values, ok := jsonProperty(root, "Operations")
	operations := jsonElements(values)
	if !ok || jsonKindOf(values) != jsonArray || len(operations) == 0 {
		return nil, scimInvalidSyntax("PatchOp Operations must be a non-empty array.")
	}
	ops := make([]scimPatchOp, 0, len(operations))
	for _, v := range operations {
		op := jsonReadString(v, "op")
		value, hasValue := jsonProperty(v, "value")
		_, hasPath := jsonProperty(v, "path")
		if op == nil || jsonKindOf(v) != jsonObject || !hasValue && !hasPath {
			return nil, scimInvalidSyntax("Each PatchOp operation must contain an op and a value or path.")
		}
		ops = append(ops, scimPatchOp{op: *op, path: jsonReadString(v, "path"), value: value})
	}
	return ops, nil
}

// scimOp is an operation's op, trimmed and lower-cased (:780-781, :835-836).
func scimOp(op string) (string, error) {
	o := strings.ToLower(dotnetTrim(op))
	if o != "add" && o != "replace" && o != "remove" {
		return "", scimInvalidSyntax("Only Add, Replace, and Remove are supported.")
	}
	return o, nil
}

// scimUserAction is ScimUserAction (:1215): one validated change to a User.
// field is userName, externalId, active, displayName, email, givenName or
// familyName; value is the text set (nil for a removal and for active);
// active is the state an active operation carried.
type scimUserAction struct {
	field  string
	value  *string
	active *bool
	remove bool
}

const nameComponentsDetail = "Only name.givenName and name.familyName are supported."

// emailPathPattern is how .NET recognised a User's work email path (:885):
// emails.value, with or without a filter, in any case. The case is spelled
// out letter by letter here and in the other patterns, since RE2's (?i)
// folds by Unicode and would let U+017F ſ stand for s, which .NET's
// patterns refuse.
var emailPathPattern = regexp.MustCompile(`^[eE][mM][aA][iI][lL][sS](?:\[.*?\])?\.[vV][aA][lL][uU][eE]$`)

// scimUserActions is TryValidateUserOperations (:774-828): every operation
// validated into actions, or the first refusal. Entra sends a pathless
// object (:784-786); each of its properties becomes the operation its name
// is the path of, a name object one per part and emails its work email, so
// the request stays atomic.
func scimUserActions(ops []scimPatchOp) ([]scimUserAction, error) {
	var actions []scimUserAction
	for _, operation := range ops {
		op, err := scimOp(operation.op)
		if err != nil {
			return nil, err
		}
		if (operation.path == nil || dotnetBlank(*operation.path)) && jsonKindOf(operation.value) == jsonObject {
			for _, p := range jsonMembers(operation.value) {
				var expanded []scimPatchOp
				switch {
				case equalOrdinalIgnoreCase(p.name, "name") && jsonKindOf(p.value) == jsonObject:
					for _, part := range jsonMembers(p.value) {
						if part.name != "givenName" && part.name != "familyName" {
							return nil, scimInvalidPath(nameComponentsDetail)
						}
						expanded = append(expanded, scimPatchOp{op: operation.op, path: apicommon.Ptr("name." + part.name), value: part.value})
					}
				case equalOrdinalIgnoreCase(p.name, "emails"):
					email, err := readWorkEmail(p.value)
					if err != nil {
						return nil, err
					}
					value := json.RawMessage("null")
					if email != nil {
						value, _ = json.Marshal(*email) // a string: Marshal cannot fail
					}
					expanded = append(expanded, scimPatchOp{op: operation.op, path: apicommon.Ptr("emails.value"), value: value})
				default:
					expanded = append(expanded, scimPatchOp{op: operation.op, path: apicommon.Ptr(p.name), value: p.value})
				}
				more, err := scimUserActions(expanded)
				if err != nil {
					return nil, err
				}
				actions = append(actions, more...)
			}
			continue
		}

		field, value, remove, err := scimUserPath(operation.path, operation.value, op)
		if err != nil {
			return nil, err
		}
		switch {
		case (field == "userName" || field == "externalId") && remove:
			return nil, scimError{400, "mutability", field + " cannot be removed."}
		case field == "userName" && !isSafeValue(value, 512):
			return nil, scimInvalidValue("userName is invalid.")
		case field == "externalId" && !isSafeValue(normalizeExternalID(value), 512):
			return nil, scimInvalidValue("externalId is invalid.")
		case (field == "displayName" || field == "givenName" || field == "familyName") && value != nil && !isSafeValue(value, 200):
			return nil, scimInvalidValue("A name value is invalid.")
		case field == "email" && value != nil && !validMailAddress(*value):
			return nil, scimInvalidValue("The work email is invalid.")
		}
		action := scimUserAction{field: field, value: value, remove: remove}
		if b, ok := jsonBool(operation.value); ok && field == "active" {
			action.active = &b
		}
		actions = append(actions, action)
	}
	return actions, nil
}

// scimUserPath is TryNormalizeUserPath (:875-912): the field a User path
// names and the text it sets. A name path must carry exactly one part.
func scimUserPath(rawPath *string, rawValue json.RawMessage, op string) (field string, value *string, remove bool, err error) {
	remove = op == "remove"
	if rawPath == nil || dotnetBlank(*rawPath) {
		if jsonKindOf(rawValue) != jsonObject {
			return "", nil, false, scimInvalidPath("A path is required for this PatchOp value.")
		}
		return "", nil, false, scimInvalidPath("Pathless object PatchOp values are not supported.")
	}
	path := dotnetTrim(*rawPath)
	switch {
	case emailPathPattern.MatchString(path):
		field = "email"
	case equalOrdinalIgnoreCase(path, "userName"):
		field = "userName"
	case equalOrdinalIgnoreCase(path, "externalId"):
		field = "externalId"
	case equalOrdinalIgnoreCase(path, "active"):
		field = "active"
	case equalOrdinalIgnoreCase(path, "displayName"):
		field = "displayName"
	case equalOrdinalIgnoreCase(path, "name.givenName"):
		field = "givenName"
	case equalOrdinalIgnoreCase(path, "name.familyName"):
		field = "familyName"
	case equalOrdinalIgnoreCase(path, "name"):
		if jsonKindOf(rawValue) != jsonObject {
			return "", nil, false, scimInvalidValue("name must be an object.")
		}
		parts := jsonMembers(rawValue)
		if len(parts) != 1 || parts[0].name != "givenName" && parts[0].name != "familyName" {
			return "", nil, false, scimInvalidPath(nameComponentsDetail)
		}
		field, rawValue = parts[0].name, parts[0].value
	default:
		return "", nil, false, scimInvalidPath("The PatchOp path is not supported.")
	}
	if remove {
		return field, nil, true, nil
	}
	if field == "active" {
		if _, ok := jsonBool(rawValue); !ok {
			return "", nil, false, scimInvalidValue("active must be boolean.")
		}
		return field, nil, false, nil
	}
	s, ok := jsonStringOf(rawValue)
	if !ok {
		return "", nil, false, scimInvalidValue("The PatchOp value must be a string.")
	}
	return field, &s, false, nil
}

// scimGroupAction is ScimGroupAction (:1216): one validated change to a
// Group. field is displayName, active or members. For members, present is
// what upstream now says of memberIDs; replace also makes every other
// member absent, and removeAll makes every member absent and lists none.
type scimGroupAction struct {
	field     string
	name      string
	active    bool
	memberIDs []string
	present   bool
	replace   bool
	removeAll bool
}

// dotnetSpace is .NET's regex \s, [\f\n\r\t\v\x85\p{Z}]. Go's \s matches
// only ASCII white space.
const dotnetSpace = `[\f\n\r\t\v\x{85}\p{Z}]`

// The two patterns .NET matched a Group's members path with (:853, :860),
// in any ASCII case.
var (
	membersPathPattern = regexp.MustCompile(strings.ReplaceAll(
		`^[mM][eE][mM][bB][eE][rR][sS](?:\[[vV][aA][lL][uU][eE]\s+[eE][qQ]\s+"[^"]+"\])?$`, `\s`, dotnetSpace))
	memberFilterPattern = regexp.MustCompile(strings.ReplaceAll(
		`[vV][aA][lL][uU][eE]\s+[eE][qQ]\s+"([^"]+)"`, `\s`, dotnetSpace))
)

// scimGroupActions is TryValidateGroupOperations (:830-873). A pathless
// object's properties are the paths members, displayName and active. A
// members path with a value filter and remove removes that one member;
// remove on members, pathless or not, removes them all (:845, :855-858).
// displayName must be a safe name of at most 200 characters, pathless or
// not; .NET checked only the pathful form, and a blank or longer pathless
// one reached its database.
func scimGroupActions(ops []scimPatchOp) ([]scimGroupAction, error) {
	var actions []scimGroupAction
	for _, operation := range ops {
		op, err := scimOp(operation.op)
		if err != nil {
			return nil, err
		}
		var path *string
		if operation.path != nil {
			path = apicommon.Ptr(dotnetTrim(*operation.path))
		}
		if path == nil && jsonKindOf(operation.value) == jsonObject {
			for _, p := range jsonMembers(operation.value) {
				name, isName := jsonStringOf(p.value)
				active, isBool := jsonBool(p.value)
				switch {
				case equalOrdinalIgnoreCase(p.name, "members"):
					ids, err := memberValues(p.value)
					if err != nil {
						return nil, err
					}
					actions = append(actions, scimGroupAction{field: "members", memberIDs: ids, present: op != "remove", replace: op == "replace", removeAll: op == "remove"})
				case equalOrdinalIgnoreCase(p.name, "displayName") && isName && isSafeValue(&name, maxDisplayNameLength):
					actions = append(actions, scimGroupAction{field: "displayName", name: name})
				case equalOrdinalIgnoreCase(p.name, "active") && isBool:
					actions = append(actions, scimGroupAction{field: "active", active: active})
				default:
					return nil, scimInvalidPath("The PatchOp path is not supported.")
				}
			}
			continue
		}
		if path != nil && membersPathPattern.MatchString(*path) {
			filtered := memberFilterPattern.FindStringSubmatch(*path)
			switch {
			case op == "remove" && equalOrdinalIgnoreCase(*path, "members"):
				actions = append(actions, scimGroupAction{field: "members", removeAll: true})
			case op == "remove" && filtered != nil:
				actions = append(actions, scimGroupAction{field: "members", memberIDs: []string{filtered[1]}})
			default:
				ids, err := memberValues(operation.value)
				if err != nil {
					return nil, err
				}
				actions = append(actions, scimGroupAction{field: "members", memberIDs: ids, present: op != "remove", replace: op == "replace", removeAll: op == "remove"})
			}
			continue
		}
		if path != nil && equalOrdinalIgnoreCase(*path, "displayName") && op != "remove" {
			if name, ok := jsonStringOf(operation.value); ok && isSafeValue(&name, maxDisplayNameLength) {
				actions = append(actions, scimGroupAction{field: "displayName", name: name})
				continue
			}
		}
		if path != nil && equalOrdinalIgnoreCase(*path, "active") && op != "remove" {
			if active, ok := jsonBool(operation.value); ok {
				actions = append(actions, scimGroupAction{field: "active", active: active})
				continue
			}
		}
		return nil, scimInvalidPath("The PatchOp path is not supported.")
	}
	return actions, nil
}

// memberValues is TryReadMemberValues (:1120-1131): an array of ids, each
// a string or an object's value.
func memberValues(v json.RawMessage) ([]string, error) {
	if jsonKindOf(v) != jsonArray {
		return nil, scimInvalidValue("members PatchOp values must be an array.")
	}
	ids := []string{}
	for _, item := range jsonElements(v) {
		id := jsonReadString(item, "value")
		if s, ok := jsonStringOf(item); ok {
			id = &s
		}
		if id == nil || dotnetBlank(*id) {
			return nil, scimInvalidValue("A group member id is required.")
		}
		ids = append(ids, *id)
	}
	return ids, nil
}

// scimBodyVersionStale is CheckMetaVersion's body form (:1178-1185): a
// PatchOp's meta.version, when it is a string, must be current.
func scimBodyVersionStale(body []byte, current string) bool {
	meta, ok := jsonProperty(body, "meta")
	if !ok {
		return false
	}
	version, ok := jsonProperty(meta, "version")
	if !ok {
		return false
	}
	s, ok := jsonStringOf(version)
	return ok && s != current
}

// scimPrecondition is CheckPrecondition (:1171-1177): If-Match, when present
// and not *, must list current, each entry trimmed of white space and then
// of quotes.
func scimPrecondition(ifMatch *string, current string) error {
	if ifMatch == nil || dotnetBlank(*ifMatch) || *ifMatch == "*" {
		return nil
	}
	for entry := range strings.SplitSeq(*ifMatch, ",") {
		if strings.Trim(dotnetTrim(entry), `"`) == current {
			return nil
		}
	}
	return scimStale
}

// queryValue is HttpRequest.Query[name] as .NET read it: the values of
// every parameter whose name equals name ignoring case, decoded as ASP.NET
// decodes a query ('+' is a space), and joined with a comma, as
// StringValues converts to a string. present is false when there is none.
func queryValue(rawQuery, name string) (value string, present bool) {
	var values []string
	for pair := range strings.SplitSeq(rawQuery, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if equalOrdinalIgnoreCase(unescapeQuery(k), name) {
			values = append(values, unescapeQuery(v))
		}
	}
	return strings.Join(values, ","), len(values) > 0
}

func unescapeQuery(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return strings.ReplaceAll(s, "+", " ")
}

// scimPaging is TryReadPaging (:1049-1057). Each parameter is read twice,
// as .NET read it: its value is ParseQueryInt's (:1165), digits only or
// else the default, and it is refused when it is present but int.TryParse
// cannot read it. So a signed or white-space-padded integer is not refused
// and counts as the default (startIndex=-5 is 1, count=+7 is 100), and
// startIndex=0 and count=101 are refused.
func scimPaging(rawQuery string) (startIndex, count int, err error) {
	startRaw, startPresent := queryValue(rawQuery, "startIndex")
	countRaw, countPresent := queryValue(rawQuery, "count")
	startIndex = parseQueryInt(startRaw, startPresent, 1)
	count = parseQueryInt(countRaw, countPresent, scimMaxPageSize)
	if startIndex < 1 || count < 0 || count > scimMaxPageSize ||
		startPresent && !dotnetIntParses(startRaw) || countPresent && !dotnetIntParses(countRaw) {
		return 0, 0, scimInvalidPaging
	}
	return startIndex, count, nil
}

// parseQueryInt is ParseQueryInt (:1165): int.TryParse with
// NumberStyles.None, which reads ASCII digits alone, within int32, and
// otherwise the fallback.
func parseQueryInt(s string, present bool, fallback int) int {
	if !present || s == "" {
		return fallback
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return fallback
		}
		n = n*10 + int(s[i]-'0')
		if n > math.MaxInt32 {
			return fallback
		}
	}
	return n
}

// dotnetIntParses reports whether int.TryParse(s) succeeds: NumberStyles.Integer,
// which allows leading and trailing white space (U+0009 to U+000D and the
// space) and one leading sign before the digits, within int32.
func dotnetIntParses(s string) bool {
	s = strings.Trim(s, "\t\n\v\f\r ")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative, s = s[0] == '-', s[1:]
	}
	if s == "" {
		return false
	}
	var n int64
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		n = n*10 + int64(s[i]-'0')
		if n > math.MaxInt32+1 {
			return false
		}
	}
	return n <= math.MaxInt32 || negative && n == math.MaxInt32+1
}

// scimFilterPattern is .NET's filter grammar, its regex exactly (:1064),
// with \s spelled as .NET means it and its case-insensitivity spelled
// letter by letter (see emailPathPattern): attr eq "json-string", joined by
// and. An attribute is ASCII letters and digits, as .NET's [A-Za-z] was:
// uſerName does not match. Like .NET's, a repeated group captures only its
// last repetition: of three or more clauses, the first and the last are
// read, and the ones between are matched but ignored, attribute and all.
var scimFilterPattern = regexp.MustCompile(strings.ReplaceAll(
	`^\s*([A-Za-z][A-Za-z0-9]*)\s+[eE][qQ]\s+("(?:\\.|[^"\\])*")(?:\s+[aA][nN][dD]\s+([A-Za-z][A-Za-z0-9]*)\s+[eE][qQ]\s+("(?:\\.|[^"\\])*"))*\s*$`,
	`\s`, dotnetSpace))

// scimFilterClause is ScimFilter (:1217): attribute equals value.
type scimFilterClause struct {
	attribute, value string
}

// scimFilter is TryReadFilter (:1059-1079): no filter when raw is blank;
// otherwise at most 512 characters matching scimFilterPattern, each
// attribute read one of allowed, ignoring case, and each value a JSON
// string.
func scimFilter(raw string, allowed ...string) ([]scimFilterClause, error) {
	if dotnetBlank(raw) {
		return nil, nil
	}
	if utf16Length(raw) > scimMaxFilterLength {
		return nil, scimInvalidFilter("The filter is too long.")
	}
	m := scimFilterPattern.FindStringSubmatchIndex(raw)
	if m == nil {
		return nil, scimInvalidFilter(`Only field eq "value" filters joined by and are supported.`)
	}
	first, err := scimFilterClauseOf(raw[m[2]:m[3]], raw[m[4]:m[5]], allowed)
	if err != nil {
		return nil, err
	}
	clauses := []scimFilterClause{first}
	if m[6] >= 0 {
		last, err := scimFilterClauseOf(raw[m[6]:m[7]], raw[m[8]:m[9]], allowed)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, last)
	}
	return clauses, nil
}

// scimFilterClauseOf is one clause: the allowed attribute attribute names,
// and quoted decoded as JSON (TryDecodeQuoted, :1169). JsonSerializer
// refused a lone surrogate escape, where encoding/json decodes it as
// U+FFFD, so escapes are checked first (pairedSurrogateEscapes).
func scimFilterClauseOf(attribute, quoted string, allowed []string) (scimFilterClause, error) {
	for _, a := range allowed {
		if !equalOrdinalIgnoreCase(a, attribute) {
			continue
		}
		var value string
		if !pairedSurrogateEscapes(quoted) || json.Unmarshal([]byte(quoted), &value) != nil {
			break
		}
		return scimFilterClause{attribute: a, value: value}, nil
	}
	return scimFilterClause{}, scimInvalidFilter("The filter field or value is invalid.")
}

// pairedSurrogateEscapes reports whether every surrogate a \u escape of
// the JSON string literal quoted names is half of a pair: a high one
// (D800-DBFF) followed at once by an escaped low one (DC00-DFFF). A lone
// half is not UTF-16 text, which .NET's reader refused. Escapes that are no
// \u escape at all are json.Unmarshal's to refuse.
func pairedSurrogateEscapes(quoted string) bool {
	for i := 0; i < len(quoted); i++ {
		if quoted[i] != '\\' {
			continue
		}
		i++ // the escaped character, never itself the start of an escape
		if i >= len(quoted) || quoted[i] != 'u' {
			continue
		}
		unit, ok := hexUnit(quoted, i+1)
		if !ok {
			continue
		}
		i += 4
		switch {
		case 0xdc00 <= unit && unit <= 0xdfff:
			return false
		case 0xd800 <= unit && unit <= 0xdbff:
			if i+6 >= len(quoted) || quoted[i+1] != '\\' || quoted[i+2] != 'u' {
				return false
			}
			low, ok := hexUnit(quoted, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// hexUnit is the UTF-16 unit the four hex digits of s at at spell.
func hexUnit(s string, at int) (uint64, bool) {
	if at+4 > len(s) {
		return 0, false
	}
	unit, err := strconv.ParseUint(s[at:at+4], 16, 16)
	return unit, err == nil
}

// scimFilterValues splits clauses into the values each of the two
// attributes must equal.
func scimFilterValues(clauses []scimFilterClause, first string) (firsts, externalIDs []string) {
	firsts, externalIDs = []string{}, []string{}
	for _, c := range clauses {
		if c.attribute == first {
			firsts = append(firsts, c.value)
		} else {
			externalIDs = append(externalIDs, c.value)
		}
	}
	return firsts, externalIDs
}

// scimMembersExcluded is IsMembersExcluded (:1166): excludedAttributes lists
// members.
func scimMembersExcluded(rawQuery string) bool {
	v, _ := queryValue(rawQuery, "excludedAttributes")
	for item := range strings.SplitSeq(v, ",") {
		if equalOrdinalIgnoreCase(dotnetTrim(item), "members") {
			return true
		}
	}
	return false
}

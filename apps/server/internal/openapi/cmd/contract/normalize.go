package main

// numericStringPatterns are the numeric-string patterns ASP.NET Core's web
// JSON defaults (NumberHandling = AllowReadingFromString) add next to
// `format` when the OpenAPI 3.1 -> 3.0 downgrade drops `type` from a number:
// int32/int64, unbounded decimal, and unbounded decimal with exponent.
var numericStringPatterns = map[string]bool{
	`^-?(?:0|[1-9]\d*)$`:                            true,
	`^-?(?:0|[1-9]\d*)(?:\.\d+)?$`:                  true,
	`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`: true,
}

// normalizeNumbers walks the decoded JSON tree in place, restoring `type` on
// schemas the 3.0 downgrade left untyped: a `format` of int32/int64 implies
// `type: integer`; float/double implies `type: number`. .NET always writes
// these as JSON numbers on the wire, so this is a shape-preserving fix, not
// a contract change — without it, oapi-codegen falls back to interface{}.
// The numeric-string `pattern` the downgrade added alongside `format`
// becomes redundant once `type` is restored, so it is dropped too; any
// other pattern is left alone. It reports how many schemas it changed.
func normalizeNumbers(v any) int {
	count := 0
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case obj:
			if _, hasType := t["type"]; !hasType {
				var newType string
				switch format, _ := t["format"].(string); format {
				case "int32", "int64":
					newType = "integer"
				case "float", "double":
					newType = "number"
				}
				if newType != "" {
					t["type"] = newType
					count++
					if pattern, ok := t["pattern"].(string); ok && numericStringPatterns[pattern] {
						delete(t, "pattern")
					}
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	return count
}

// normalizeNullableRefs walks the decoded JSON tree in place, rewriting a
// nullable schema whose only content is a one-element `oneOf` around a bare
// `$ref` into the 3.0 idiom for "this reference, or null": `allOf` with
// `nullable: true`. Both spellings carry the same wire shape, but
// oapi-codegen generates a json.RawMessage union wrapper (with As.../From...
// helpers) for the one-element oneOf and a plain pointer for the allOf. Any
// other `oneOf` (more than one element, or not nullable) is left untouched.
// It reports how many schemas it changed.
func normalizeNullableRefs(v any) int {
	count := 0
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case obj:
			if nullable, _ := t["nullable"].(bool); nullable {
				if oneOf, ok := t["oneOf"].([]any); ok && len(oneOf) == 1 {
					if elem, ok := oneOf[0].(obj); ok && len(elem) == 1 {
						if ref, ok := elem["$ref"].(string); ok {
							delete(t, "oneOf")
							delete(t, "type")
							t["allOf"] = []any{obj{"$ref": ref}}
							count++
						}
					}
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	return count
}

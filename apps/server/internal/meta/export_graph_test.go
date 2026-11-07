package meta

// GrClassify is classifyGraphError over a refusal made of its parts, for the
// table that drives Messenger design N12's error map.
func GrClassify(status, code, subcode int, tagged bool) error {
	return classifyGraphError(graphError{Status: status, Code: code, Subcode: subcode, Message: "Meta's words"}, tagged)
}

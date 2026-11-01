package operations

// The codes of Error, stable: the assistant records and branches on them.
const (
	// CodeNotReady: the dispatcher has not been bound to a composed API.
	CodeNotReady = "not_ready"
	// CodeUnknownOperation: no operation has the id.
	CodeUnknownOperation = "operation_unknown"
	// CodeNever: the operation's mode is never.
	CodeNever = "operation_never"
	// CodeConfirmationRequired: the operation's mode is confirm and the call
	// was not Confirmed.
	CodeConfirmationRequired = "confirmation_required"
	// CodeNoGrant: the call's context carries no assistant grant or no user.
	CodeNoGrant = "no_grant"
	// CodeInvalidRequest: the call does not fit the operation (a missing path
	// parameter, a parameter it does not declare, a body that is not JSON).
	CodeInvalidRequest = "invalid_request"
	// CodeCallFailed: the operation's handler panicked.
	CodeCallFailed = "call_failed"
)

// Error is a refusal before dispatch. It is not the router's answer: a 4xx or
// 5xx that came back from the operation is a CallResult.
type Error struct {
	// Code is one of the Code constants.
	Code string
	// Message says what to change, in terms safe to show the model.
	Message string
}

func (e *Error) Error() string { return "operations: " + e.Message }

// Is lets errors.Is(err, operations.ErrNever) match on the code alone.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrNotReady             = &Error{Code: CodeNotReady}
	ErrUnknownOperation     = &Error{Code: CodeUnknownOperation}
	ErrNever                = &Error{Code: CodeNever}
	ErrConfirmationRequired = &Error{Code: CodeConfirmationRequired}
	ErrNoGrant              = &Error{Code: CodeNoGrant}
	ErrInvalidRequest       = &Error{Code: CodeInvalidRequest}
	ErrCallFailed           = &Error{Code: CodeCallFailed}
)

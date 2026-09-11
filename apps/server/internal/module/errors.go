package module

import "errors"

// ErrNotImplemented marks a handler stub that has not been built yet, so a
// module under construction can wire every contract operation to a real
// http.HandlerFunc (satisfying Router.Err's "never registered" check) before
// its behaviour exists. ResponseError maps it to a 501, never a 500.
var ErrNotImplemented = errors.New("not implemented")

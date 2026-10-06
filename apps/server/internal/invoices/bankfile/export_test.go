package bankfile

// Finish is finish, for the guarantee no parser's input can reach today:
// a file of no accounts still answers a non-nil slice.
var Finish = (*File).finish

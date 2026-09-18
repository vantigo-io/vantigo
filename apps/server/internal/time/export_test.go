package timetracking

// InLockedTx exposes inLockedTx to the external tests, whose fake
// directories record any call made from inside a withLockedTx transaction.
var InLockedTx = inLockedTx

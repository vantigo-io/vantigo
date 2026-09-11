package identity

// Every gen.StrictServerInterface operation not yet implemented answered 501
// through module.ResponseError, with its stub here. Each area moved the
// operations it implemented into its own file and deleted them here, so the
// build kept proving the interface complete. No stub is left: SCIM was the
// last area. The test package's pendingOperations lists exactly these
// operations, none; Task 20 deletes this file and that list.

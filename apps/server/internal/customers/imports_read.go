package customers

import "github.com/vantigo-io/vantigo/server/internal/fileimport"

// This file is the bounds of the file a person uploads (customers import
// wizard design D2). Reading it — the encoding, the separator, the quoting,
// the formula guard — is internal/fileimport's (energy import design D2), which
// the energy import shares; the caps and the upload's own refusal are this
// module's. Whatever the reader decides is recorded on the import and said on
// the page, and the mapping table shows the columns it made, so a wrong guess
// is seen before a row is written.

// The upload's bounds (design D2). maxImportUploadRequestBytes is the router's
// cap for the operation (module.RouterOptions.BodyLimits): the file plus room
// for the multipart framing, the attachment upload's margin.
const (
	maxImportUploadBytes        = 50 * 1024 * 1024
	maxImportUploadRequestBytes = maxImportUploadBytes + 64*1024
	maxImportUploadRows         = 100_000
	maxImportUploadColumns      = 200
	// importSampleRows is how many rows the import carries as its sample: the
	// preview the mapping step is judged on.
	importSampleRows = 20
)

// importUploadLimits are the bounds above as the reader takes them.
var importUploadLimits = fileimport.Limits{
	MaxBytes:   maxImportUploadBytes,
	MaxRows:    maxImportUploadRows,
	MaxColumns: maxImportUploadColumns,
}

// invalidImportUploadMessage is the refusal of an upload the reader never
// sees: no file part, two, an empty one or one past the size cap.
const invalidImportUploadMessage = "A customer import is one CSV file of at most 50 MB, sent as the multipart part named 'file'"

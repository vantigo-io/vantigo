package staff

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// This file reads and judges an upload's multipart body (design S14): the
// file part and the three small fields beside it, each refusal reported on
// its field so a caller sees every problem with the request at once.

// maxDocumentFieldBytes bounds a text field of the upload — an id or a date
// — so a field is never buffered past what one can hold.
const maxDocumentFieldBytes = 64

// The upload's text fields as the contract names them.
const (
	documentCategoryField = "categoryId"
	documentValidToField  = "validTo"
	documentSignedAtField = "signedAt"
)

// documentForm is one upload's body as read: the file's bytes, the name and
// type it was sent under, and the text fields. tooLarge and unreadable say
// why the file could not be had.
type documentForm struct {
	data               []byte
	fileName, declared string
	files              int
	tooLarge           bool
	unreadable         bool
	fields             map[string]string
}

// readDocumentForm reads every part of mr. The file is read up to
// maxDocumentBytes+1 bytes — enough to tell "too large" from "just within"
// without buffering an unbounded body; a read the router's own request cap
// stops (http.MaxBytesError) is too large as well, and any other failure an
// unreadable body.
func readDocumentForm(mr *multipart.Reader) documentForm {
	f := documentForm{fields: map[string]string{}}
	if mr == nil {
		f.unreadable = true
		return f
	}
	fail := func(err error) documentForm {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			f.tooLarge = true
		} else {
			f.unreadable = true
		}
		return f
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			return fail(err)
		}
		switch name := part.FormName(); name {
		case documentFormField:
			f.files++
			f.fileName, f.declared = part.FileName(), part.Header.Get("Content-Type")
			data, err := io.ReadAll(io.LimitReader(part, maxDocumentBytes+1))
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			if len(data) > maxDocumentBytes {
				f.tooLarge = true
				return f
			}
			f.data = data
		case documentCategoryField, documentValidToField, documentSignedAtField:
			value, err := io.ReadAll(io.LimitReader(part, maxDocumentFieldBytes+1))
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			f.fields[name] = string(value)
		default:
			_ = part.Close()
		}
	}
}

// parsedUpload is one upload judged: the type its bytes are, the category it
// names and its dates.
type parsedUpload struct {
	kind       documentType
	categoryID int64
	validTo    *openapi_types.Date
	signedAt   *openapi_types.Date
}

// The file's refusals. The type's message is one for every way the bytes and
// what they were called disagree, so the refusal never tells a caller which
// probe worked.
const (
	documentTypeMessage = "A document must be a PDF, JPEG, PNG or HEIC file, and its contents must match the type and file name it is sent under"
	documentSizeMessage = "A document is at most 10 MiB"
)

// parseUpload judges f: the file is there once, within the bound, one of the
// four types and not contradicted by its name or declared type; the category
// is an id; each date is a date. Every failure is collected.
func parseUpload(f documentForm) (parsedUpload, fieldErrors) {
	var out parsedUpload
	var errs fieldErrors
	switch {
	case f.tooLarge:
		errs.addCoded(documentFormField, codeDocumentTooLarge, documentSizeMessage)
	case f.unreadable:
		errs.add(documentFormField, "The request's body could not be read as a multipart form")
	case f.files > 1:
		errs.add(documentFormField, "A request files one document; this one carried more than one file")
	case f.files == 0 || len(f.data) == 0:
		errs.add(documentFormField, "A document file is required")
	default:
		kind, ok := documentTypeOf(f.declared, f.fileName, f.data)
		if !ok {
			errs.addCoded(documentFormField, codeDocumentTypeUnsupported, documentTypeMessage)
		}
		out.kind = kind
	}
	if raw, ok := f.fields[documentCategoryField]; !ok || strings.TrimSpace(raw) == "" {
		errs.add(documentCategoryField, "A category is required")
	} else if id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err != nil || id < 1 {
		errs.add(documentCategoryField, fmt.Sprintf("A category is a category's id, but was '%s'", raw))
	} else {
		out.categoryID = id
	}
	var msg string
	out.validTo, msg = formDate("A valid-to date", f.fields, documentValidToField)
	errs.add(documentValidToField, msg)
	out.signedAt, msg = formDate("A signing date", f.fields, documentSignedAtField)
	errs.add(documentSignedAtField, msg)
	return out, errs
}

// formDate is an optional date field of the form, nil when it is not sent.
func formDate(label string, fields map[string]string, name string) (*openapi_types.Date, string) {
	raw, ok := fields[name]
	if !ok {
		return nil, ""
	}
	day, err := time.Parse(time.DateOnly, strings.TrimSpace(raw))
	if err != nil {
		return nil, label + " is a date such as 2026-09-30"
	}
	return &openapi_types.Date{Time: day}, ""
}

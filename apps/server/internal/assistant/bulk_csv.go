package assistant

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// The failed and unknown items of a bulk job as a file (S13, S17): for each, who
// it is, which values fed the change, its status, what the router answered and the problem's own code and
// title — never a response body. Semicolon-separated, UTF-8 with a byte order
// mark and CRLF, as the other exports are, and written with encoding/csv so the
// quoting is the library's. A cell that would open as a formula in a
// spreadsheet is neutralised.

// csvCell neutralises a cell a spreadsheet would run: one that begins with =, +,
// -, @, a tab or a carriage return gets an apostrophe in front, as the other
// exports do.
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		return "'" + value
	}
	return value
}

// boundReference names the values that fed the change for an item, in parameter
// order, so a person can find it again: id=42.
func boundReference(item bulkItem) string {
	names := make([]string, 0, len(item.Bound))
	for name := range item.Bound {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		var text string
		switch v := item.Bound[name].(type) {
		case string:
			text = v
		case json.Number:
			text = v.String()
		case float64:
			text = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			text = strconv.FormatBool(v)
		}
		parts = append(parts, name+"="+text)
	}
	return strings.Join(parts, ", ")
}

// unknownItemSentence says what an unknown item is, in the file's problem title
// column, as the card says it.
const unknownItemSentence = "This change may or may not have been made; check it"

// failuresCSV renders the failed items.
func failuresCSV(rows []store.AssistantBulkJobItem) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	w.UseCRLF = true
	if err := w.Write([]string{"item", "reference", "status", "http_status", "problem_code", "problem_title"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		var item bulkItem
		_ = json.Unmarshal(row.Item, &item)
		httpStatus := ""
		if row.HttpStatus != nil {
			httpStatus = strconv.Itoa(int(*row.HttpStatus))
		}
		code, title := "", ""
		if row.Status == "unknown" {
			title = unknownItemSentence
		}
		if row.ProblemCode != nil {
			code = *row.ProblemCode
		}
		if row.ProblemTitle != nil {
			title = *row.ProblemTitle
		}
		if err := w.Write([]string{csvCell(item.Label), csvCell(boundReference(item)), row.Status, httpStatus, csvCell(code), csvCell(title)}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// csvResponse is the 200 of the failed items: a text/csv attachment.
type csvResponse struct {
	name string
	body []byte
}

func (r csvResponse) VisitGetAssistantBulkJobFailuresResponse(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+r.name+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

// GetAssistantBulkJobFailures (GET /api/v1/assistant/bulk-jobs/{id}/failures.csv)
func (s *server) GetAssistantBulkJobFailures(ctx context.Context, req gen.GetAssistantBulkJobFailuresRequestObject) (gen.GetAssistantBulkJobFailuresResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.GetAssistantBulkJobFailuresResponseObject](r, err)
	}
	job, r, err := s.bulkJobOf(ctx, req.Id)
	if err != nil || r != nil {
		return orRefusal[gen.GetAssistantBulkJobFailuresResponseObject](r, err)
	}
	rows, err := s.failedItems(ctx, job)
	if err != nil {
		return nil, err
	}
	body, err := failuresCSV(rows)
	if err != nil {
		return nil, err
	}
	return csvResponse{name: "assistant-bulk-" + job.ID.String() + "-failed-items.csv", body: body}, nil
}

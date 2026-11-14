package accounting

// The operations later commits of this delivery build answer 501 until then.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

func (s *server) GetAccountingReport(context.Context, gen.GetAccountingReportRequestObject) (gen.GetAccountingReportResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ExportAccountingReport(context.Context, gen.ExportAccountingReportRequestObject) (gen.ExportAccountingReportResponseObject, error) {
	return nil, module.ErrNotImplemented
}

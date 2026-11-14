package accounting

// The seam of delivery E: the SAF-T operations its lanes fill in.

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

var errNotYet = errors.New("accounting: not built yet")

func (s *server) ListAccountingSaftExports(context.Context, gen.ListAccountingSaftExportsRequestObject) (gen.ListAccountingSaftExportsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CreateAccountingSaftExport(context.Context, gen.CreateAccountingSaftExportRequestObject) (gen.CreateAccountingSaftExportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CheckAccountingSaftExport(context.Context, gen.CheckAccountingSaftExportRequestObject) (gen.CheckAccountingSaftExportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingSaftExport(context.Context, gen.GetAccountingSaftExportRequestObject) (gen.GetAccountingSaftExportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DownloadAccountingSaftExport(context.Context, gen.DownloadAccountingSaftExportRequestObject) (gen.DownloadAccountingSaftExportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingSaftImports(context.Context, gen.ListAccountingSaftImportsRequestObject) (gen.ListAccountingSaftImportsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UploadAccountingSaftImport(context.Context, gen.UploadAccountingSaftImportRequestObject) (gen.UploadAccountingSaftImportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingSaftImport(context.Context, gen.GetAccountingSaftImportRequestObject) (gen.GetAccountingSaftImportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ApplyAccountingSaftImport(context.Context, gen.ApplyAccountingSaftImportRequestObject) (gen.ApplyAccountingSaftImportResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DownloadAccountingSaftImportFile(context.Context, gen.DownloadAccountingSaftImportFileRequestObject) (gen.DownloadAccountingSaftImportFileResponseObject, error) {
	return nil, errNotYet
}

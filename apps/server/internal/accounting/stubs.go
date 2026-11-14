package accounting

// The operations later commits of this delivery build answer 501 until then.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

func (s *server) ListAccountingAccounts(context.Context, gen.ListAccountingAccountsRequestObject) (gen.ListAccountingAccountsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) CreateAccountingAccount(context.Context, gen.CreateAccountingAccountRequestObject) (gen.CreateAccountingAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) DeleteAccountingAccount(context.Context, gen.DeleteAccountingAccountRequestObject) (gen.DeleteAccountingAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetAccountingAccount(context.Context, gen.GetAccountingAccountRequestObject) (gen.GetAccountingAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) UpdateAccountingAccount(context.Context, gen.UpdateAccountingAccountRequestObject) (gen.UpdateAccountingAccountResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PutAccountingAccountGroupings(context.Context, gen.PutAccountingAccountGroupingsRequestObject) (gen.PutAccountingAccountGroupingsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ListAccountingGroupingCodes(context.Context, gen.ListAccountingGroupingCodesRequestObject) (gen.ListAccountingGroupingCodesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetAccountingLock(context.Context, gen.GetAccountingLockRequestObject) (gen.GetAccountingLockResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PutAccountingLock(context.Context, gen.PutAccountingLockRequestObject) (gen.PutAccountingLockResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ListAccountingOpenItems(context.Context, gen.ListAccountingOpenItemsRequestObject) (gen.ListAccountingOpenItemsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) SettleAccountingOpenItem(context.Context, gen.SettleAccountingOpenItemRequestObject) (gen.SettleAccountingOpenItemResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetAccountingOpening(context.Context, gen.GetAccountingOpeningRequestObject) (gen.GetAccountingOpeningResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PostAccountingOpening(context.Context, gen.PostAccountingOpeningRequestObject) (gen.PostAccountingOpeningResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetAccountingReport(context.Context, gen.GetAccountingReportRequestObject) (gen.GetAccountingReportResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ExportAccountingReport(context.Context, gen.ExportAccountingReportRequestObject) (gen.ExportAccountingReportResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) AssignAccountingRole(context.Context, gen.AssignAccountingRoleRequestObject) (gen.AssignAccountingRoleResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ListAccountingTaxCodes(context.Context, gen.ListAccountingTaxCodesRequestObject) (gen.ListAccountingTaxCodesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) UpdateAccountingTaxCode(context.Context, gen.UpdateAccountingTaxCodeRequestObject) (gen.UpdateAccountingTaxCodeResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) AddAccountingTaxCodeRate(context.Context, gen.AddAccountingTaxCodeRateRequestObject) (gen.AddAccountingTaxCodeRateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ListAccountingVouchers(context.Context, gen.ListAccountingVouchersRequestObject) (gen.ListAccountingVouchersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PostAccountingVoucher(context.Context, gen.PostAccountingVoucherRequestObject) (gen.PostAccountingVoucherResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) PreviewAccountingVoucher(context.Context, gen.PreviewAccountingVoucherRequestObject) (gen.PreviewAccountingVoucherResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) GetAccountingVoucher(context.Context, gen.GetAccountingVoucherRequestObject) (gen.GetAccountingVoucherResponseObject, error) {
	return nil, module.ErrNotImplemented
}

func (s *server) ReverseAccountingVoucher(context.Context, gen.ReverseAccountingVoucherRequestObject) (gen.ReverseAccountingVoucherResponseObject, error) {
	return nil, module.ErrNotImplemented
}

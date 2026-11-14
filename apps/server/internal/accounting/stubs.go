package accounting

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

// errSalesNotBuilt answers delivery B's operations until the next commits of
// this series build them.
var errSalesNotBuilt = errors.New("accounting: sales posting is not built yet")

func (s *server) ListAccountingPostingRules(context.Context, gen.ListAccountingPostingRulesRequestObject) (gen.ListAccountingPostingRulesResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) CreateAccountingPostingRule(context.Context, gen.CreateAccountingPostingRuleRequestObject) (gen.CreateAccountingPostingRuleResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) UpdateAccountingPostingRule(context.Context, gen.UpdateAccountingPostingRuleRequestObject) (gen.UpdateAccountingPostingRuleResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) DeleteAccountingPostingRule(context.Context, gen.DeleteAccountingPostingRuleRequestObject) (gen.DeleteAccountingPostingRuleResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) ListAccountingUnposted(context.Context, gen.ListAccountingUnpostedRequestObject) (gen.ListAccountingUnpostedResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) RetryAccountingUnposted(context.Context, gen.RetryAccountingUnpostedRequestObject) (gen.RetryAccountingUnpostedResponseObject, error) {
	return nil, errSalesNotBuilt
}

func (s *server) GetAccountingCatchUp(context.Context, gen.GetAccountingCatchUpRequestObject) (gen.GetAccountingCatchUpResponseObject, error) {
	return nil, errSalesNotBuilt
}

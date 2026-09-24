package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// customerReferenceKindProjects is the one kind of customer reference this
// module holds: projects.projects.customer_id, the customer a project bills to.
const customerReferenceKindProjects = "projects.projects"

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every project of
// the absorbed one bills to the survivor from then on. Time and expenses reach
// a customer only through a project, so this one statement keeps them right
// too — neither holds a customer id of its own.
type customerReferenceHolder struct {
	clock func() time.Time
}

var _ contracts.CustomerReferenceHolder = (*customerReferenceHolder)(nil)

// newCustomerReferenceHolder is Module's CustomerReferences.
func newCustomerReferenceHolder(d module.Deps) contracts.CustomerReferenceHolder {
	return &customerReferenceHolder{clock: d.Clock}
}

// RepointCustomer moves every project of from to into, inside the caller's
// transaction (RepointProjectsCustomer). No project timeline entry is
// written: the merge is recorded on the survivor's customer timeline, and a
// project's customerName reads the survivor's the moment the merge commits.
//
// A customer merged into itself moves nothing: the merge refuses that case
// (merge_self) before any holder runs, and this guard keeps it so should that
// ever change — the statement would match every project the customer has and
// bump each one's revision for nothing.
func (h *customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	if from == into {
		return []contracts.RepointedReferences{{Kind: customerReferenceKindProjects, Count: 0}}, nil
	}
	n, err := store.New(tx).RepointProjectsCustomer(ctx, store.RepointProjectsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into, Now: h.clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("projects: re-point customer %d's projects to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: customerReferenceKindProjects, Count: n}}, nil
}

package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The four surfaces as they come off the wire, decoded only as far as these
// tests read them. They are deliberately hand-written rather than generated:
// what is being checked is that two modules' *published* shapes agree, and a
// generated type of either module's would make one of them the definition.

// summaryResponse decodes ExpensesProjectSummaryResponse.
type summaryResponse struct {
	Currencies []struct {
		Currency       string  `json:"currency"`
		Approved       bucket  `json:"approved"`
		Submitted      bucket  `json:"submitted"`
		Draft          bucket  `json:"draft"`
		Total          bucket  `json:"total"`
		ReadyCount     int32   `json:"readyCount"`
		ReadyAmount    float64 `json:"readyAmount"`
		InvoicedCount  int32   `json:"invoicedCount"`
		InvoicedAmount float64 `json:"invoicedAmount"`
		UnpricedCount  int32   `json:"unpricedCount"`
	} `json:"currencies"`
	LastEntryDate   *string `json:"lastEntryDate"`
	ProjectCurrency *string `json:"projectCurrency"`
	Capabilities    struct {
		CanRecord bool `json:"canRecord"`
	} `json:"capabilities"`
}

// currency picks one currency's figures out of the summary, or fails.
func (s summaryResponse) currency(t *testing.T, code string) struct {
	Currency       string  `json:"currency"`
	Approved       bucket  `json:"approved"`
	Submitted      bucket  `json:"submitted"`
	Draft          bucket  `json:"draft"`
	Total          bucket  `json:"total"`
	ReadyCount     int32   `json:"readyCount"`
	ReadyAmount    float64 `json:"readyAmount"`
	InvoicedCount  int32   `json:"invoicedCount"`
	InvoicedAmount float64 `json:"invoicedAmount"`
	UnpricedCount  int32   `json:"unpricedCount"`
} {
	t.Helper()
	for _, c := range s.Currencies {
		if c.Currency == code {
			return c
		}
	}
	t.Fatalf("the summary holds no %s among %+v", code, s.Currencies)
	return s.Currencies[0]
}

// summaryDate is the summary's last entry date, or a failed test.
func summaryDate(t *testing.T, s summaryResponse) string {
	t.Helper()
	if s.LastEntryDate == nil {
		t.Fatal("the summary reports no last entry date on a project with expenses")
	}
	return *s.LastEntryDate
}

func readSummary(t *testing.T, c *modtest.Client, projectID int32) summaryResponse {
	t.Helper()
	var s summaryResponse
	okJSON(t, c, http.MethodGet, fmt.Sprintf(expensesProjectSum, projectID), nil, &s)
	return s
}

// economyResponse decodes the part of ProjectEconomyResponse this delivery
// touches.
type economyResponse struct {
	Currency        *string `json:"currency"`
	TimeTracking    bool    `json:"timeTracking"`
	ExpenseTracking bool    `json:"expenseTracking"`
	BudgetUsed      float64 `json:"budgetUsed"`
	OverBudget      bool    `json:"overBudget"`
	Expenses        *struct {
		Approved        economyBucket `json:"approved"`
		Submitted       economyBucket `json:"submitted"`
		Draft           economyBucket `json:"draft"`
		TotalCost       float64       `json:"totalCost"`
		TotalAmount     float64       `json:"totalAmount"`
		ReadyCount      int32         `json:"readyCount"`
		ReadyAmount     float64       `json:"readyAmount"`
		InvoicedCount   int32         `json:"invoicedCount"`
		InvoicedAmount  float64       `json:"invoicedAmount"`
		UnpricedCount   int32         `json:"unpricedCount"`
		LastEntryDate   *string       `json:"lastEntryDate"`
		OtherCurrencies []struct {
			Currency    string  `json:"currency"`
			Count       int32   `json:"count"`
			Cost        float64 `json:"cost"`
			Amount      float64 `json:"amount"`
			ReadyAmount float64 `json:"readyAmount"`
		} `json:"otherCurrencies"`
	} `json:"expenses"`
}

func readEconomy(t *testing.T, c *modtest.Client, projectID int32) economyResponse {
	t.Helper()
	var e economyResponse
	okJSON(t, c, http.MethodGet, fmt.Sprintf(projectEconomyPath, projectID), nil, &e)
	return e
}

// economyBucket is the same three figures under the names the *projects*
// contract gives them: what Expenses publishes as billAmount, Projects
// publishes as amount. The two modules name the field differently on purpose —
// each uses its own vocabulary — which is exactly why a test that compares
// them has to spell both out rather than share one type.
type economyBucket struct {
	Count  int32   `json:"count"`
	Cost   float64 `json:"cost"`
	Amount float64 `json:"amount"`
}

// asBucket is the economy's bucket in the shape every assertion here compares.
func (b economyBucket) asBucket() bucket {
	return bucket{Count: b.Count, Cost: b.Cost, BillAmount: b.Amount}
}

// portfolioResponse decodes the part of the portfolio this delivery touches.
type portfolioResponse struct {
	Data   []portfolioRow `json:"data"`
	Totals struct {
		ReadyCount        int32 `json:"readyCount"`
		ReadyExpenseCount int32 `json:"readyExpenseCount"`
		ReadyAmounts      []struct {
			Currency      string  `json:"currency"`
			Amount        float64 `json:"amount"`
			ExpenseAmount float64 `json:"expenseAmount"`
			TotalAmount   float64 `json:"totalAmount"`
		} `json:"readyAmounts"`
	} `json:"totals"`
	TimeTracking    bool `json:"timeTracking"`
	ExpenseTracking bool `json:"expenseTracking"`
}

type portfolioRow struct {
	Project struct {
		Id int32 `json:"id"`
	} `json:"project"`
	Currency           *string `json:"currency"`
	ReadyCount         int32   `json:"readyCount"`
	ReadyAmount        float64 `json:"readyAmount"`
	ReadyExpenseCount  int32   `json:"readyExpenseCount"`
	ReadyExpenseAmount float64 `json:"readyExpenseAmount"`
	ReadyTotalAmount   float64 `json:"readyTotalAmount"`
}

// row picks one project's row out of the portfolio, or fails.
func (p portfolioResponse) row(t *testing.T, projectID int32) portfolioRow {
	t.Helper()
	for _, r := range p.Data {
		if r.Project.Id == projectID {
			return r
		}
	}
	t.Fatalf("project %d has no row in the portfolio (%d rows)", projectID, len(p.Data))
	return portfolioRow{}
}

func readPortfolio(t *testing.T, c *modtest.Client) portfolioResponse {
	t.Helper()
	var p portfolioResponse
	okJSON(t, c, http.MethodGet, projectsEconomy, nil, &p)
	return p
}

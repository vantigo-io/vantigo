import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { meta, outlay } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { stubExpensesApi } from "../test/server";
import { ExpenseFormModal } from "./-expense-form-modal";

// With the books kept in Accounting (accounting design A20, R2), the supplier
// invoice kind shows what the host passes: a pointer to Accounting's
// supplier invoices. This package knows nothing of Accounting.

describe("the supplier invoice kind with Accounting on", () => {
  it("shows the host's notice on the supplier invoice kind only", async () => {
    stubExpensesApi({ entries: [outlay()], meta: meta() });
    renderWithProviders(
      <ExpenseFormModal
        state={{ mode: "create" }}
        onClose={() => {}}
        supplierInvoiceNotice={<p>Enter supplier invoices in Accounting</p>}
      />,
    );
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    expect(within(dialog).queryByText("Enter supplier invoices in Accounting")).not.toBeInTheDocument();
    await userEvent.click(await within(dialog).findByRole("radio", { name: "Supplier invoice" }));
    expect(await within(dialog).findByText("Enter supplier invoices in Accounting")).toBeInTheDocument();
  });

  it("shows nothing without one", async () => {
    stubExpensesApi({ entries: [outlay()], meta: meta() });
    renderWithProviders(<ExpenseFormModal state={{ mode: "create" }} onClose={() => {}} />);
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    await userEvent.click(await within(dialog).findByRole("radio", { name: "Supplier invoice" }));
    expect(within(dialog).queryByText(/Accounting/)).not.toBeInTheDocument();
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { EntryDetails } from "../components/entry-details";
import { sent } from "../test/api";
import {
  APPROVER,
  attachment,
  capabilities,
  categoriesWithSubcontractor,
  meta,
  OTHER,
  outlay,
  projectOptions,
  supplierInvoice,
  withReceipts,
} from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { renderRoute } from "../test/route-tree";
import { type ExpensesServer, stubExpensesApi } from "../test/server";

/** Mounts My expenses over the given server and opens the create form. */
const openNew = async (server: ExpensesServer) => {
  const fetchMock = stubExpensesApi(server);
  renderRoute("/expenses");
  await screen.findByRole("table", { name: "My expenses" });
  await userEvent.click(screen.getByRole("button", { name: "New expense" }));
  return { dialog: await screen.findByRole("dialog", { name: "New expense" }), fetchMock };
};

/** Mounts My expenses over the given server and opens one expense for editing. */
const openEdit = async (server: ExpensesServer, description: string) => {
  const fetchMock = stubExpensesApi(server);
  renderRoute("/expenses");
  const row = (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;
  await userEvent.click(within(row).getByRole("button", { name: `Edit ${description}` }));
  return { dialog: await screen.findByRole("dialog", { name: "Edit the expense" }), fetchMock };
};

const choose = async (dialog: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(dialog).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

const withSubcontractor = meta({ categories: categoriesWithSubcontractor });

describe("a supplier invoice in the expense form", () => {
  it("is offered beside outlay and mileage only where there are projects", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: meta({ projectsAvailable: false }) });

    expect(within(dialog).getByRole("radio", { name: "Outlay" })).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Mileage" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: "Supplier invoice" })).not.toBeInTheDocument();
  });

  it("asks for the supplier's fields, starts under Subcontractor and billable, and names no payer", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));

    expect(within(dialog).getByRole("textbox", { name: "Invoice date" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Invoice number" })).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Due date" })).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Category" })).toHaveValue("Subcontractor");
    expect(within(dialog).queryByRole("radio", { name: "The company paid" })).not.toBeInTheDocument();
    expect(within(dialog).getByTestId("company-pays")).toHaveTextContent("The company pays a supplier invoice");
    // Its picker is the projects whose money the caller may see, not the bookable ones.
    await within(dialog).findByRole("combobox", { name: "Project" });
    await choose(dialog, "Project", /KVEM1000/);
    expect(within(dialog).getByRole("switch", { name: "Billable" })).toBeChecked();
    expect(
      fetchMock.actualCalls.some(([url]) => String(url).endsWith("/api/v1/expenses/projects?kind=supplier_invoice")),
    ).toBe(true);
  });

  it("sends the supplier's own fields and no payer, and asks for the invoice once it is a draft", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    await within(dialog).findByRole("combobox", { name: "Project" });
    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier" }), "Rør & Varme AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Invoice number" }), "F-20260918");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    const body = sent(fetchMock, "POST").body;
    expect(body).toMatchObject({
      kind: "supplier_invoice",
      supplier: "Rør & Varme AS",
      invoiceNumber: "F-20260918",
      categoryId: 14,
      currency: "NOK",
      grossAmount: 12500,
      projectId: 1001,
      billable: true,
    });
    expect(body).not.toHaveProperty("paidBy");
    expect(body).not.toHaveProperty("dueDate");
    // A new one stays open as a draft, because its document can only be
    // attached to something that exists — and it says the document is needed.
    expect(await within(dialog).findByTestId("attach-supplier-invoice")).toHaveTextContent(
      "Attach the supplier's invoice",
    );
    expect(within(dialog).getByText("The supplier's invoice")).toBeInTheDocument();
    // The submit would be refused without the document, so it is not offered.
    expect(within(dialog).getByRole("button", { name: "Save and submit" })).toBeDisabled();
    expect(within(dialog).getByTestId("submit-needs-invoice")).toHaveTextContent("then it can be submitted");
    await userEvent.upload(
      await within(dialog).findByLabelText("Add receipts"),
      new File(["%PDF-1.7"], "faktura.pdf", { type: "application/pdf" }),
    );
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Save and submit" })).toBeEnabled());
    expect(within(dialog).queryByTestId("submit-needs-invoice")).not.toBeInTheDocument();
  });

  it("drops a project the supplier-invoice picker does not offer when the kind changes", async () => {
    const { dialog } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      projects: projectOptions,
      supplierInvoiceProjects: [projectOptions[1]],
    });

    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    const project = await within(dialog).findByRole("combobox", { name: "Project" });
    await waitFor(() => expect(project).toHaveValue(""));
  });

  it("keeps a project both pickers offer when the kind changes", async () => {
    const { dialog } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      projects: projectOptions,
      supplierInvoiceProjects: [projectOptions[0]],
    });

    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    expect(await within(dialog).findByRole("combobox", { name: "Project" })).toHaveValue("KVEM1000 · Kverneland web");
  });

  it("refuses to save without the supplier, the invoice number and the project", async () => {
    const { dialog, fetchMock } = await openNew({
      entries: [outlay()],
      meta: withSubcontractor,
      supplierInvoiceProjects: projectOptions,
    });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Supplier invoice" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    expect(await within(dialog).findByText("Name the supplier")).toBeInTheDocument();
    expect(within(dialog).getByText("Give the supplier's invoice number")).toBeInTheDocument();
    expect(within(dialog).getByText("A supplier invoice is booked on a project")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("carries its number and due date through an edit, still naming no payer", async () => {
    const { dialog, fetchMock } = await openEdit(
      { entries: [supplierInvoice()], meta: withSubcontractor, supplierInvoiceProjects: projectOptions },
      "Rørleggerarbeid, uke 38",
    );

    const number = within(dialog).getByRole("textbox", { name: "Invoice number" });
    expect(number).toHaveValue("F-20260918");
    await userEvent.clear(number);
    await userEvent.type(number, "F-20260919");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/551"));
    const body = sent(fetchMock, "PUT").body;
    expect(body).toMatchObject({
      kind: "supplier_invoice",
      invoiceNumber: "F-20260919",
      dueDate: "2026-10-18",
      projectId: 1001,
      revision: 1,
    });
    expect(body).not.toHaveProperty("paidBy");
  });
});

describe("a supplier invoice read out", () => {
  it("names the supplier's number and due date, and says when it is past due and not invoiced", () => {
    renderWithProviders(<EntryDetails expense={supplierInvoice({ dueDate: "2026-01-02" })} />);

    expect(screen.getByText("Supplier invoice")).toBeInTheDocument();
    expect(screen.getByText("Invoice date")).toBeInTheDocument();
    expect(screen.getByText("F-20260918")).toBeInTheDocument();
    expect(screen.getByText("Due date")).toBeInTheDocument();
    expect(screen.getByText("Overdue")).toBeInTheDocument();
    expect(screen.getByText("The supplier's invoice")).toBeInTheDocument();
  });

  it("says nothing of overdue before the due date, or once the line is invoiced", () => {
    const { unmount } = renderWithProviders(<EntryDetails expense={supplierInvoice({ dueDate: "2999-12-31" })} />);
    expect(screen.queryByText("Overdue")).not.toBeInTheDocument();
    unmount();

    renderWithProviders(
      <EntryDetails
        expense={supplierInvoice({
          dueDate: "2026-01-02",
          capabilities: capabilities({ canSeeBilling: true }),
          billing: { billAmount: 10000, invoice: { at: "2026-02-01T10:00:00Z", by: APPROVER } },
        })}
      />,
    );
    expect(screen.queryByText("Overdue")).not.toBeInTheDocument();
  });
});

describe("a supplier invoice in the approval queue", () => {
  it("is labelled by its kind", async () => {
    stubExpensesApi({
      meta: meta({ capabilities: { canApprove: true, canViewAll: false, canManage: false } }),
      entries: [
        withReceipts(
          supplierInvoice({
            id: 701,
            status: "submitted",
            submittedAt: "2026-09-19T10:00:00Z",
            owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
            capabilities: capabilities({ canApprove: true }),
          }),
          [attachment({ fileName: "faktura.pdf", contentType: "application/pdf" })],
        ),
      ],
    });
    renderRoute("/expenses/approvals");

    const table = await screen.findByRole("table", { name: "Grace Hopper's expenses" });
    const line = within(table).getByText("Rørleggerarbeid, uke 38").closest("tr") as HTMLElement;
    expect(within(line).getByText("Supplier invoice")).toBeInTheDocument();
    expect(within(line).getByText("1 receipt")).toBeInTheDocument();
  });
});

describe("a supplier invoice in My expenses", () => {
  it("is listed under its own kind, paid by the company, and is a kind the list filters by", async () => {
    stubExpensesApi({ entries: [outlay(), supplierInvoice()] });
    const { router } = renderRoute("/expenses");

    const row = (await screen.findByText("Rørleggerarbeid, uke 38")).closest("[data-expense]") as HTMLElement;
    expect(within(row).getByText("Supplier invoice")).toBeInTheDocument();
    expect(within(row).getByText("The company paid")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    await userEvent.click(await screen.findByRole("option", { name: "Supplier invoice" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ kind: "supplier_invoice" }));
    await waitFor(() => expect(screen.queryByText("Taxi to the airport")).not.toBeInTheDocument());
    expect(screen.getByText("Rørleggerarbeid, uke 38")).toBeInTheDocument();
  });
});

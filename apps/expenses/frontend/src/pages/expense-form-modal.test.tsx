import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { attachment, capabilities, meta, outlay, projectOptions, withReceipts } from "../test/fixtures";
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

const noProjects = meta({ projectsAvailable: false });

const receipt = (name = "receipt.jpg", type = "image/jpeg") => new File(["bytes"], name, { type });

describe("the expense form", () => {
  it("fills the VAT out of the gross at the chosen rate and shows the net", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: noProjects });

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "625");
    await userEvent.click(within(dialog).getByRole("radio", { name: "25 %" }));

    expect(within(dialog).getByRole("textbox", { name: "VAT" })).toHaveValue("125");
    expect(within(dialog).getByText(/Net: /)).toHaveTextContent("500.00");
  });

  it("lets a hand-typed VAT win, and forgets the helper's choice when it does", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: noProjects });

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "625");
    await userEvent.click(within(dialog).getByRole("radio", { name: "25 %" }));
    const vat = within(dialog).getByRole("textbox", { name: "VAT" });
    await userEvent.clear(vat);
    await userEvent.type(vat, "100");

    expect(within(dialog).getByRole("radio", { name: "25 %" })).not.toBeChecked();
    expect(within(dialog).getByRole("radio", { name: "None" })).toBeChecked();
    expect(within(dialog).getByText(/Net: /)).toHaveTextContent("525.00");
  });

  it("sends a mileage line's own fields and none of an outlay's", async () => {
    const { dialog, fetchMock } = await openNew({ entries: [outlay()], meta: noProjects });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Mileage" }));
    expect(within(dialog).queryByRole("combobox", { name: "Category" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Amount including VAT" })).not.toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Site visit");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), "Stavanger");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "To" }), "Bryne");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Distance in kilometres" }), "120");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    const body = sent(fetchMock, "POST").body;
    expect(body).toMatchObject({ kind: "mileage", distanceKm: 120, fromPlace: "Stavanger", toPlace: "Bryne" });
    for (const forbidden of ["categoryId", "supplier", "paidBy", "grossAmount", "vatAmount", "currency"]) {
      expect(body).not.toHaveProperty(forbidden);
    }
  });

  it("previews what a mileage line is worth at the rate in force on its date, as a preview", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: noProjects });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Mileage" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Distance in kilometres" }), "120");

    const preview = await within(dialog).findByTestId("mileage-preview");
    expect(preview).toHaveTextContent("120 km × 5.30");
    expect(preview).toHaveTextContent("636.00");
    expect(within(dialog).getByText(/A preview/)).toBeInTheDocument();
  });

  it("warns before the save when no mileage rate applies on the date", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: noProjects, rates: [] });

    await userEvent.click(within(dialog).getByRole("radio", { name: "Mileage" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Distance in kilometres" }), "120");

    expect(await within(dialog).findByText(/No mileage rate applies on this date/)).toBeInTheDocument();
  });

  it("has no project block at all in an installation without projects", async () => {
    const { dialog, fetchMock } = await openNew({ entries: [outlay()], meta: noProjects });

    expect(within(dialog).queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("switch", { name: "Billable" })).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/expenses/projects"))).toBe(false);
  });

  it("offers the project, its billing lines and the billable switch when projects are on", async () => {
    const { dialog, fetchMock } = await openNew({ entries: [outlay()], projects: projectOptions });

    await choose(dialog, "Project", /KVEM1000/);
    await choose(dialog, "Line", /PM/);
    await userEvent.click(within(dialog).getByRole("switch", { name: "Billable" }));

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Train ticket");
    await choose(dialog, "Category", "Travel");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "420");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    expect(sent(fetchMock, "POST").body).toMatchObject({ projectId: 1001, billingLineId: 3001, billable: true });
  });

  it("never offers the customer's figures on a new expense — the project prices it afterwards", async () => {
    const { dialog } = await openNew({
      entries: [outlay()],
      projects: projectOptions,
      meta: meta({ capabilities: { canApprove: true, canViewAll: true, canManage: true } }),
    });

    await choose(dialog, "Project", /KVEM1000/);
    await userEvent.click(within(dialog).getByRole("switch", { name: "Billable" }));

    expect(within(dialog).queryByRole("textbox", { name: "Markup" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Customer rate per kilometre" })).not.toBeInTheDocument();
    expect(within(dialog).getByText(/set from the project when the expense is priced/)).toBeInTheDocument();
  });

  it("shows the customer's figures as they stand, read only, to whoever may see them", async () => {
    const { dialog } = await openEdit(
      {
        entries: [
          outlay({
            billable: true,
            project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
            capabilities: capabilities({ canEdit: true, canSeeBilling: true }),
            billing: { billAmount: 600, markupPercent: 20 },
          }),
        ],
        projects: projectOptions,
      },
      "Taxi to the airport",
    );

    const billing = within(dialog).getByTestId("expense-billing");
    expect(billing).toHaveTextContent("600.00");
    expect(billing).toHaveTextContent("20");
    expect(within(dialog).queryByRole("textbox", { name: "Markup" })).not.toBeInTheDocument();
  });

  it("replaces an expense with the revision the form was opened at, echoing what it loaded", async () => {
    const { dialog, fetchMock } = await openEdit(
      {
        entries: [
          outlay({
            revision: 4,
            billable: true,
            project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
            billingLine: { id: 3001, code: "PM" },
          }),
        ],
        projects: projectOptions,
      },
      "Taxi to the airport",
    );

    const description = within(dialog).getByRole("textbox", { name: "Description" });
    await userEvent.clear(description);
    await userEvent.type(description, "Taxi home");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/501"));
    const body = sent(fetchMock, "PUT").body;
    expect(body).toMatchObject({
      revision: 4,
      description: "Taxi home",
      projectId: 1001,
      billingLineId: 3001,
      billable: true,
      categoryId: 11,
      paidBy: "employee",
    });
    // The markup and the customer rate are the project's figures; a form that
    // names one is refused on that field.
    expect(body).not.toHaveProperty("markupPercent");
    expect(body).not.toHaveProperty("billRatePerKm");
  });

  it("puts a refusal on the input it names", async () => {
    const { dialog } = await openNew({
      entries: [outlay()],
      meta: noProjects,
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/entries"
          ? problemResponse(400, "Invalid expense", { description: ["A description holds at most 500 characters"] })
          : undefined,
    });

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Train ticket");
    await choose(dialog, "Category", "Travel");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "420");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    expect(await within(dialog).findByText("A description holds at most 500 characters")).toBeInTheDocument();
  });

  it("asks for the draft to be saved before receipts can be added, then adds and removes one", async () => {
    const { dialog } = await openNew({ entries: [outlay()], meta: noProjects });

    expect(within(dialog).getByText(/Save the expense as a draft first/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Add receipts")).not.toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Train ticket");
    await choose(dialog, "Category", "Travel");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "420");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    const input = await within(dialog).findByLabelText("Add receipts");
    await userEvent.upload(input, receipt("ticket.jpg"));

    expect(await within(dialog).findByRole("img", { name: "ticket.jpg" })).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove ticket.jpg" }));
    await waitFor(() => expect(within(dialog).queryByRole("img", { name: "ticket.jpg" })).not.toBeInTheDocument());
  });

  it("refuses a file the server would refuse anyway, without uploading it", async () => {
    const { dialog, fetchMock } = await openEdit(
      { entries: [withReceipts(outlay(), [attachment()])], meta: noProjects },
      "Taxi to the airport",
    );

    // applyAccept: false — the accept attribute is a hint a file dialog uses,
    // and a dropped file never passes through one.
    await userEvent.upload(within(dialog).getByLabelText("Add receipts"), receipt("notes.txt", "text/plain"), {
      applyAccept: false,
    });

    expect(await within(dialog).findByText(/notes.txt is not a JPEG/)).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/attachments"))).toBe(false);
  });

  it("shows the receipt rule the server answered a submit with", async () => {
    const { dialog } = await openEdit(
      {
        entries: [outlay({ grossAmount: 2400, vatAmount: 0 })],
        meta: noProjects,
        write: (method, path) =>
          method === "POST" && path === "/api/v1/expenses/submit"
            ? problemResponse(400, "Invalid submission", {
                entryIds: [
                  "Expense 501 needs a receipt: an outlay the employee paid for more than 1250.00 cannot be submitted without one",
                ],
              })
            : undefined,
      },
      "Taxi to the airport",
    );

    await userEvent.click(within(dialog).getByRole("button", { name: "Save and submit" }));

    expect(await within(dialog).findByText(/Expense 501 needs a receipt/)).toBeInTheDocument();
  });
});

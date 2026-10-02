import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { customerSearch, jsonResponse, problemResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { draft, listPage, meta } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const path = (input: RequestInfo | URL) => String(input);

/** The fetch fake: meta, the list (paged by its query), the customers list and a create. */
const server = (options: { canCreate?: boolean; totalPages?: number } = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(
        200,
        meta({
          capabilities: {
            canCreate: options.canCreate ?? true,
            canIssue: true,
            canManage: false,
            canRegisterPayments: true,
            canSend: true,
          },
        }),
      );
    }
    if (url.startsWith("/api/v1/invoices?") || url === "/api/v1/invoices") {
      if (method === "POST") return jsonResponse(201, draft({ id: 1010, lines: [] }));
      return jsonResponse(200, listPage({ totalPages: options.totalPages ?? 1 }));
    }
    if (url.startsWith("/api/v1/customers?")) return customerSearch(url);
    if (url === "/api/v1/invoices/1010") return jsonResponse(200, draft({ id: 1010, lines: [] }));
    return new Response(null, { status: 404 });
  });

describe("the invoice list", () => {
  it("shows drafts first and names each document's customer", async () => {
    server();
    renderRoute("/invoices");

    const rows = await screen.findAllByRole("row");
    expect(within(rows[1]).getByText("Kari Nordmann")).toBeInTheDocument();
    expect(within(rows[1]).getAllByText("Draft").length).toBeGreaterThan(0);
    expect(within(rows[2]).getByText("1001")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Credit note")).toBeInTheDocument();
    expect(screen.getByText("3 documents")).toBeInTheDocument();
  });

  it("asks the server for the chosen status and the next page", async () => {
    const fetchMock = server({ totalPages: 2 });
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    await userEvent.click(screen.getByRole("radio", { name: "Draft" }));
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => path(url).includes("status=draft"))).toBe(true));
    await userEvent.click(await screen.findByRole("button", { name: "2" }));
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => path(url).includes("page=2"))).toBe(true));
  });

  it("keeps the page shown while the next filter's is fetched", async () => {
    let answer: (response: Response) => void = () => {};
    const drafts = new Promise<Response>((resolve) => {
      answer = resolve;
    });
    stubFetch((input: RequestInfo | URL) => {
      const url = path(input);
      if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
      if (url.includes("status=draft")) return drafts;
      if (url.startsWith("/api/v1/invoices?")) return jsonResponse(200, listPage());
      if (url.startsWith("/api/v1/customers?")) return customerSearch(url);
      return new Response(null, { status: 404 });
    });
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    await userEvent.click(screen.getByRole("radio", { name: "Draft" }));
    // The drafts are still on their way: the last page stays, no skeleton.
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.queryByTestId("content-skeleton")).not.toBeInTheDocument();
    answer(jsonResponse(200, listPage({ totalCount: 1 })));
    expect(await screen.findByText("1 document")).toBeInTheDocument();
  });

  it("searches once the typing pauses, not per keystroke", async () => {
    const fetchMock = server();
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "Acme");
    await waitFor(() => expect(fetchMock.actualCalls.some(([url]) => path(url).includes("search=Acme"))).toBe(true));
    const searches = fetchMock.actualCalls.filter(([url]) => /^\/api\/v1\/invoices\?.*search=/.test(path(url)));
    expect(searches.map(([url]) => path(url))).toEqual(["/api/v1/invoices?page=1&search=Acme"]);
  });

  // An archived customer's issued invoices are kept (bookkeeping act § 13),
  // so the filter finds them too; only a new draft's buyer must be active.
  it("filters by any customer, a disabled or an archived one too", async () => {
    const fetchMock = server();
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    await userEvent.click(screen.getByRole("combobox", { name: "Customer" }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => path(url).startsWith("/api/v1/customers?"))).toBe(true),
    );
    const customers = fetchMock.actualCalls
      .map(([url]) => path(url))
      .filter((url) => url.startsWith("/api/v1/customers?"));
    expect(customers.every((url) => !url.includes("status=") && url.includes("includeArchived=true"))).toBe(true);
  });

  it.each([
    ["may pick no buyer", true, false],
    ["may create no drafts", false, true],
  ] as const)("offers no New invoice to a caller who %s", async (_who, canCreate, canViewCustomers) => {
    server({ canCreate });
    const { queryClient } = renderRoute("/invoices", { canViewCustomers });
    await screen.findByText("Kari Nordmann");
    // Only once meta has answered does the page know what the caller may do.
    await waitFor(() => expect(queryClient.getQueryState(["invoices", "meta"])?.status).toBe("success"));
    expect(screen.queryByRole("button", { name: "New invoice" })).not.toBeInTheDocument();
  });

  it("offers New invoice to a caller who may create drafts and pick a buyer", async () => {
    server();
    renderRoute("/invoices");
    expect(await screen.findByRole("button", { name: "New invoice" })).toBeInTheDocument();
  });

  it("creates a draft for the picked customer, prefilled with today and our reference, and opens it", async () => {
    const fetchMock = server();
    const { router } = renderRoute("/invoices");

    await userEvent.click(await screen.findByRole("button", { name: "New invoice" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Customer" }));
    await userEvent.click(await screen.findByRole("option", { name: "Acme AS (10001)" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create the draft" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1010"));
    // The buyer is picked among the active customers only; the gates refuse the rest.
    const buyers = fetchMock.actualCalls.map(([url]) => path(url)).filter((url) => url.includes("status=active"));
    expect(buyers.length).toBeGreaterThan(0);
    expect(buyers.some((url) => url.includes("includeArchived"))).toBe(false);
    expect(sent(fetchMock, "POST").body).toEqual({
      customerId: 2001,
      lines: [],
      deliveryDate: "2026-09-12",
      ourReference: "Ola Nordmann",
    });
  });

  it("badges each row with its state and shows an issued invoice's open amount", async () => {
    server();
    renderRoute("/invoices");

    const rows = await screen.findAllByRole("row");
    expect(screen.getByRole("columnheader", { name: "State" })).toBeInTheDocument();
    expect(within(rows[1]).getByTestId("state-badge")).toHaveTextContent("Draft");
    expect(within(rows[2]).getByTestId("state-badge")).toHaveTextContent("Issued");
    expect(within(rows[3]).getByTestId("state-badge")).toHaveTextContent("Open");
    // Only an issued invoice has an open amount; a draft and a credit note show none.
    expect(within(rows[3]).getByTestId("open-amount")).toHaveTextContent(/NOK\s?124\.99/);
    expect(within(rows[1]).getByTestId("open-amount")).toHaveTextContent("—");
    expect(within(rows[2]).getByTestId("open-amount")).toHaveTextContent("—");
  });

  it.each([
    ["Open", "open"],
    ["Partially paid", "partially_paid"],
    ["Overdue", "overdue"],
    ["Paid", "paid"],
    ["Credited", "credited"],
  ])("asks the server for the %s state", async (label, state) => {
    const fetchMock = server();
    renderRoute("/invoices");
    await screen.findByText("Kari Nordmann");

    const chips = screen.getByRole("radiogroup", { name: "State" });
    await userEvent.click(within(chips).getByRole("radio", { name: label }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => path(url) === `/api/v1/invoices?page=1&state=${state}`)).toBe(true),
    );
    // "Any state" asks for the list without the filter again.
    const asked = fetchMock.actualCalls.findIndex(([url]) => path(url).includes(`state=${state}`));
    await userEvent.click(within(chips).getByRole("radio", { name: "Any state" }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.slice(asked + 1).some(([url]) => path(url) === "/api/v1/invoices?page=1")).toBe(
        true,
      ),
    );
  });

  it("says when the list could not be loaded", async () => {
    stubFetch((input: RequestInfo | URL) =>
      path(input) === "/api/v1/invoices/meta"
        ? jsonResponse(200, meta())
        : problemResponse(500, "Internal Server Error"),
    );
    renderRoute("/invoices");
    expect(await screen.findByText("Could not load the invoices")).toBeInTheDocument();
  });
});

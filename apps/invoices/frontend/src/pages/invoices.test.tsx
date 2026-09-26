import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse, problemResponse, sent } from "../test/api";
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
        meta({ capabilities: { canCreate: options.canCreate ?? true, canIssue: true, canManage: false } }),
      );
    }
    if (url.startsWith("/api/v1/invoices?") || url === "/api/v1/invoices") {
      if (method === "POST") return jsonResponse(201, draft({ id: 1010, lines: [] }));
      return jsonResponse(200, listPage({ totalPages: options.totalPages ?? 1 }));
    }
    if (url.startsWith("/api/v1/customers?")) {
      return jsonResponse(200, { data: [{ id: 2001, name: "Acme AS", customerNumber: 10001, status: "active" }] });
    }
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

  it("offers New invoice only to a caller who may create drafts and pick a buyer", async () => {
    server();
    renderRoute("/invoices", { canViewCustomers: false });
    await screen.findByText("Kari Nordmann");
    expect(screen.queryByRole("button", { name: "New invoice" })).not.toBeInTheDocument();
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

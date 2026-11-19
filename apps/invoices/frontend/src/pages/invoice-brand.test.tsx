import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse, path, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { draft, meta, vatCodes } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

const NORDLYS = "00000000-0000-4000-8000-0000000000a2";
const DEFAULT = "00000000-0000-4000-8000-0000000000a1";

const options = [
  { id: DEFAULT, name: "Kraft-Verket", isDefault: true },
  { id: NORDLYS, name: "Nordlys", isDefault: false },
];

/** The fetch fake for one draft, its brand options and a save that echoes the draft. */
const server = (doc: InvoiceDocument) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url === "/api/v1/invoices/brands") return jsonResponse(200, options);
    if (url === "/api/v1/invoices/1001" && method === "GET") return jsonResponse(200, doc);
    if (url === "/api/v1/invoices/1001" && method === "PUT") {
      return jsonResponse(200, { ...doc, revision: doc.revision + 1 });
    }
    return new Response(null, { status: 404 });
  });

/** The visible input of the Brand select (Mantine also renders a hidden one under the same label). */
const brandField = async () => {
  const fields = await screen.findAllByLabelText("Brand");
  return fields.find((el) => el.getAttribute("type") !== "hidden") ?? fields[0];
};

describe("the editor's brand field", () => {
  it("offers the published brands and sends brandId only for a real choice", async () => {
    const fetchMock = server(
      draft({ brand: { id: DEFAULT, name: "Kraft-Verket", source: "default", unavailable: false } }),
    );
    renderRoute("/invoices/1001");

    const field = await brandField();
    expect(field).toHaveValue("Kraft-Verket");
    expect(screen.getByText(/legal name, organisation number and bank account stay the same/)).toBeInTheDocument();
    expect(screen.queryByTestId("brand-notice")).not.toBeInTheDocument();

    // Saving after another edit, without touching the field, leaves the brand to the server.
    await userEvent.type(await screen.findByRole("textbox", { name: "Line 2 description" }), "!");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").body.revision).toBe(3));
    expect(sent(fetchMock, "PUT").body).not.toHaveProperty("brandId");

    // The person switches the brand for this one document.
    await userEvent.click(await brandField());
    await userEvent.click(await screen.findByRole("option", { name: "Nordlys", hidden: true }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT")).toHaveLength(2));
    const puts = fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(puts[1][1]?.body)).brandId).toBe(NORDLYS);
  });

  it("says when the draft's brand is archived and the issue will fall back", async () => {
    server(draft({ brand: { id: NORDLYS, name: "Nordlys", source: "customer", unavailable: true } }));
    renderRoute("/invoices/1001");

    expect(await screen.findByTestId("brand-notice")).toHaveTextContent("archived or no longer published");
    expect(await brandField()).toHaveValue("Nordlys (archived)");
  });
});

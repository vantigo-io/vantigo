import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Order, Quote } from "../api/types";
import { commonAnswers, fakeServer, jsonResponse } from "../test/api";
import { meta, order, quote, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

const DEFAULT = "00000000-0000-4000-8000-0000000000b1";
const NORDLYS = "00000000-0000-4000-8000-0000000000b2";

const options = [
  { id: DEFAULT, name: "Kraft-Verket", isDefault: true },
  { id: NORDLYS, name: "Nordlys", isDefault: false },
];

const quoteUrl = "/api/v1/orders/quotes/1001";
const orderUrl = "/api/v1/orders/orders/2001";

/** The server for one document, its brand options and a save that echoes the document. */
const server = (document: Quote | Order, kind: "quote" | "order") => {
  const url = kind === "quote" ? quoteUrl : orderUrl;
  return fakeServer((call) => {
    if (call.url === "/api/v1/orders/brands") return jsonResponse(200, options);
    if (call.url === url && call.method === "GET") return jsonResponse(200, document);
    if (call.url === url && call.method === "PUT") return jsonResponse(200, document);
    if (call.method !== "GET") return undefined;
    return commonAnswers(call, settings(), meta());
  });
};

/** The visible input of the Brand select (Mantine also renders a hidden one under the same label). */
const brandField = async () => {
  const fields = await screen.findAllByLabelText("Brand");
  return fields.find((el) => el.getAttribute("type") !== "hidden") ?? fields[0];
};

describe("the editor's brand field", () => {
  it("offers the published brands and sends brandId only for a real choice", async () => {
    const fake = server(
      quote({ brand: { id: DEFAULT, name: "Kraft-Verket", source: "default", unavailable: false } }),
      "quote",
    );
    renderRoute("/orders/quotes/1001");

    expect(await brandField()).toHaveValue("Kraft-Verket");
    expect(screen.getByText(/legal name, organisation number and bank account stay the same/)).toBeInTheDocument();
    expect(screen.queryByTestId("brand-notice")).not.toBeInTheDocument();

    // Saving after another edit, without touching the field, leaves the brand to the server.
    await userEvent.type(await screen.findByLabelText("Your reference"), "!");
    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.sent("PUT", quoteUrl)).toBeDefined());
    expect(fake.sent("PUT", quoteUrl)?.body).not.toHaveProperty("brandId");

    // The person switches the brand for this one document.
    await userEvent.click(await brandField());
    await userEvent.click(await screen.findByRole("option", { name: "Nordlys", hidden: true }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.count("PUT", quoteUrl)).toBe(2));
    expect(fake.sent("PUT", quoteUrl)?.body).toMatchObject({ brandId: NORDLYS });
  });

  it("says when the draft's brand is archived and the send will fall back", async () => {
    server(quote({ brand: { id: NORDLYS, name: "Nordlys", source: "customer", unavailable: true } }), "quote");
    renderRoute("/orders/quotes/1001");

    expect(await screen.findByTestId("brand-notice")).toHaveTextContent("archived or no longer published");
    expect(await brandField()).toHaveValue("Nordlys (archived)");
  });

  it("says so too when the draft's brand is no longer offered by the list", async () => {
    server(
      order({
        brand: { id: "00000000-0000-4000-8000-0000000000b9", name: "Gone", source: "group", unavailable: false },
      }),
      "order",
    );
    renderRoute("/orders/orders/2001");

    expect(await screen.findByTestId("brand-notice")).toBeInTheDocument();
  });

  it("shows a sent quote's snapshot, fixed, with no notice", async () => {
    server(
      quote({
        status: "sent",
        brand: {
          id: "00000000-0000-4000-8000-0000000000b9",
          name: "Retired brand",
          source: "customer",
          unavailable: false,
        },
      }),
      "quote",
    );
    renderRoute("/orders/quotes/1001");

    const field = await brandField();
    expect(field).toHaveValue("Retired brand");
    expect(field).toBeDisabled();
    expect(screen.queryByTestId("brand-notice")).not.toBeInTheDocument();
  });
});

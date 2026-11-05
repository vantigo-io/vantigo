import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { renderRoute } from "../test/render";

const template = {
  id: 61,
  name: "Standard montering",
  introText: "Takk for forespørselen.",
  closingText: "",
  discountVisible: true,
  archived: false,
  revision: 2,
  validityDays: 14,
  updatedAt: "2026-11-01T08:00:00Z",
  lines: [
    { id: 71, position: 1, kind: "section", description: "Arbeid", optional: false },
    {
      id: 72,
      position: 2,
      kind: "product",
      variantId: 301,
      sku: "KAB-10",
      name: "Kabel 10 m",
      description: "",
      quantity: 2,
      unitPrice: null,
      optional: true,
    },
  ],
};

const server = (writes: (call: { method: string; url: string }) => Response | undefined = () => undefined) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === "/api/v1/orders/templates")
      return jsonResponse(200, {
        items: [
          {
            id: 61,
            name: "Standard montering",
            archived: false,
            lineCount: 2,
            validityDays: 14,
            updatedAt: "2026-11-01T08:00:00Z",
          },
        ],
      });
    if (call.method === "GET" && call.url === "/api/v1/orders/templates/61") return jsonResponse(200, template);
    if (call.method !== "GET") return writes(call);
    return commonAnswers(call);
  });

describe("the quote templates", () => {
  it("lists the templates and opens one with its lines", async () => {
    server();
    renderRoute("/orders/templates");

    await userEvent.click(await screen.findByText("Standard montering"));
    const editor = await screen.findByTestId("template-editor");
    expect(within(editor).getByLabelText(/^Name/)).toHaveValue("Standard montering");
    expect(within(editor).getByLabelText("Heading of section 1")).toHaveValue("Arbeid");
    expect(within(editor).getByRole("checkbox", { name: "Line 2 is optional" })).toBeChecked();
  });

  it("saves a changed template with the revision it read", async () => {
    const fake = server((call) =>
      call.method === "PUT" ? jsonResponse(200, { ...template, name: "Montering", revision: 3 }) : undefined,
    );
    renderRoute("/orders/templates");

    await userEvent.click(await screen.findByText("Standard montering"));
    const name = within(await screen.findByTestId("template-editor")).getByLabelText(/^Name/);
    await userEvent.clear(name);
    await userEvent.type(name, "Montering");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("PUT", "/api/v1/orders/templates/61")).toBeDefined());
    expect(fake.sent("PUT", "/api/v1/orders/templates/61")?.body).toEqual({
      name: "Montering",
      validityDays: 14,
      discountVisible: true,
      introText: "Takk for forespørselen.",
      closingText: "",
      archived: false,
      revision: 2,
      lines: [
        { id: 71, kind: "section", description: "Arbeid" },
        { id: 72, kind: "product", variantId: 301, description: "", quantity: 2, unitPrice: null, optional: true },
      ],
    });
    expect(await screen.findByText("The template is saved")).toBeInTheDocument();
  });

  it("creates a new template, which needs a name", async () => {
    const fake = server((call) =>
      call.method === "POST" ? jsonResponse(201, { ...template, id: 62, name: "Ny", lines: [] }) : undefined,
    );
    renderRoute("/orders/templates");

    await userEvent.click(await screen.findByRole("button", { name: "New template" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Give the template a name.")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/^Name/), "Ny");
    await userEvent.click(screen.getByRole("button", { name: "Note" }));
    await userEvent.type(screen.getByLabelText("Text of note 1"), "Prisene gjelder i 14 dager");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(fake.sent("POST", "/api/v1/orders/templates")).toBeDefined());
    expect(fake.sent("POST", "/api/v1/orders/templates")?.body).toMatchObject({
      name: "Ny",
      lines: [{ kind: "note", description: "Prisene gjelder i 14 dager" }],
    });
  });

  it("says a stale save as a conflict", async () => {
    server((call) => (call.method === "PUT" ? refusal(409, "version_conflict") : undefined));
    renderRoute("/orders/templates");

    await userEvent.click(await screen.findByText("Standard montering"));
    await userEvent.type(within(await screen.findByTestId("template-editor")).getByLabelText(/^Name/), "!");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByTestId("conflict-alert")).toBeInTheDocument();
  });

  it("offers no edits without orders:settings", async () => {
    server();
    renderRoute("/orders/templates", { canManageSettings: false });

    await userEvent.click(await screen.findByText("Standard montering"));
    await screen.findByTestId("template-editor");
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New template" })).not.toBeInTheDocument();
  });
});

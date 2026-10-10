import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { type Answer, requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { CURRENT_USER_ID, listPage, meta, OTHER_USER_ID, reminderPolicy } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";
import { CustomerInvoicesPanel } from "./customer-invoices-panel";

const POLICY = "/api/v1/invoices/customers/2001/reminder-policy";

/** No charges, with a note, set by another user. */
const noCharges = () =>
  reminderPolicy({
    mode: "no_charges",
    note: "Avtalt med daglig leder",
    updatedAt: "2026-09-01T10:00:00Z",
    updatedBy: OTHER_USER_ID,
  });

/** The fetch fake: meta, the customer's list, the policy as `policy` gives it, and each write by "METHOD url". */
const server = (policy: () => unknown = noCharges, answers: Record<string, Answer> = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith("/api/v1/invoices?")) return jsonResponse(200, listPage());
    if (url === POLICY && method === "GET") return jsonResponse(200, policy());
    return new Response(null, { status: 404 });
  });

/** The customer's Invoices tab as the host mounts it, with or without `invoices:payments`. */
const renderPanel = (canChange: boolean) =>
  renderAtHost(
    <CustomerInvoicesPanel
      customerId={2001}
      canCreate={false}
      canChangeReminderPolicy={canChange}
      currentUserId={CURRENT_USER_ID}
    />,
  );

describe("ReminderPolicyCard_ModesAndNote", () => {
  it("shows the policy to a reader with invoices:access, with its note and who set it, and nothing to change", async () => {
    server();
    renderPanel(false);

    const card = await screen.findByTestId("reminder-policy-card");
    expect(await within(card).findByTestId("policy-mode")).toHaveTextContent("No charges");
    expect(within(card).getByText("Letters without a fee, compensation or interest.")).toBeInTheDocument();
    expect(within(card).getByText("Note: Avtalt med daglig leder")).toBeInTheDocument();
    expect(within(card).getByText(/^Set .* by another user$/)).toBeInTheDocument();
    expect(within(card).queryByRole("radio")).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("says a customer without a policy is normal", async () => {
    server(() => reminderPolicy());
    renderPanel(false);

    expect(await screen.findByTestId("policy-mode")).toHaveTextContent("Normal");
    expect(screen.queryByText(/^Set /)).not.toBeInTheDocument();
  });

  it("lets a caller with invoices:payments change the mode and the note", async () => {
    // The server holds what was saved, so the read after the write answers it.
    let held = noCharges();
    const fetchMock = server(() => held, {
      [`PUT ${POLICY}`]: () => {
        held = reminderPolicy({
          mode: "none",
          note: "Konkurs",
          updatedAt: "2026-09-12T10:00:00Z",
          updatedBy: CURRENT_USER_ID,
        });
        return jsonResponse(200, held);
      },
    });
    renderPanel(true);

    const card = await screen.findByTestId("reminder-policy-card");
    expect(await within(card).findByRole("radio", { name: /^No charges/ })).toBeChecked();
    const save = within(card).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await userEvent.click(within(card).getByRole("radio", { name: /^No reminders/ }));
    const note = within(card).getByRole("textbox", { name: /^Note/ });
    await userEvent.clear(note);
    await userEvent.type(note, "Konkurs");
    await userEvent.click(save);

    expect(await screen.findByText("Reminder policy saved")).toBeInTheDocument();
    expect(requestTo(fetchMock, "PUT", POLICY)).toEqual({ mode: "none", note: "Konkurs" });
    await waitFor(() => expect(within(card).getByText(/^Set .* by you$/)).toBeInTheDocument());
  });

  it("says the 404 in words: no document here, or anonymised", async () => {
    server(noCharges, { [`PUT ${POLICY}`]: () => new Response(null, { status: 404 }) });
    renderPanel(true);

    const card = await screen.findByTestId("reminder-policy-card");
    await userEvent.click(await within(card).findByRole("radio", { name: /^Normal/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText(
        "This customer has no invoice or draft here, or is anonymised, so no reminder policy can be set.",
      ),
    ).toBeInTheDocument();
  });

  it("puts a refused note's words on its input", async () => {
    server(noCharges, {
      [`PUT ${POLICY}`]: () =>
        jsonResponse(400, { title: "Invalid", status: 400, errors: { note: ["A note holds at most 500 characters"] } }),
    });
    renderPanel(true);

    const card = await screen.findByTestId("reminder-policy-card");
    await userEvent.click(await within(card).findByRole("radio", { name: /^Normal/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(await within(card).findByText("At most 500 characters.")).toBeInTheDocument();
  });
});

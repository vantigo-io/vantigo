import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import type { StubbedFetch } from "../test/fetch";
import { assignableUsers, personRates } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";

/** The `search` every read of the assignable users carried, in order. */
const searchesFor = (fetchMock: StubbedFetch): (string | null)[] =>
  fetchMock.actualCalls
    .map(([input]) => new URL(String(input), "http://localhost"))
    .filter((url) => url.pathname === "/api/v1/time/rates/assignable-users")
    .map((url) => url.searchParams.get("search"));

/** Types a date the way a person does, in the format the field shows. */
const typeDate = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  await userEvent.type(field, text);
  await userEvent.tab();
};

const confirm = async (title: string, button: string) => {
  const dialog = await screen.findByRole("dialog", { name: title });
  await userEvent.click(within(dialog).getByRole("button", { name: button }));
};

describe("SettingsPage", () => {
  it("locks the period once the change is confirmed", async () => {
    const fetchMock = stubTimeApi({ rates: personRates });
    renderRoute("/time/settings");

    await typeDate(await screen.findByLabelText("Locked before"), "Oct 1, 2026");
    await userEvent.click(screen.getByRole("button", { name: "Save the lock" }));
    const dialog = await screen.findByRole("dialog", { name: "Change the lock?" });
    expect(dialog).toHaveTextContent("Oct 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toEqual({ url: "/api/v1/time/settings", body: { lockedBefore: "2026-10-01" } }),
    );
    expect(await screen.findByText("Lock saved")).toBeInTheDocument();
  });

  it("removes the lock", async () => {
    const fetchMock = stubTimeApi({ rates: personRates, settings: { lockedBefore: "2026-09-01" } });
    renderRoute("/time/settings");

    await waitFor(() => expect(screen.getByLabelText("Locked before")).toHaveValue("Sep 1, 2026"));
    await userEvent.click(screen.getByRole("button", { name: "Clear the lock" }));
    await userEvent.click(screen.getByRole("button", { name: "Save the lock" }));
    await confirm("Change the lock?", "Save");

    await waitFor(() =>
      expect(sent(fetchMock, "PUT")).toEqual({ url: "/api/v1/time/settings", body: { lockedBefore: null } }),
    );
  });

  it("lists every person's rate cards by the day each takes effect", async () => {
    stubTimeApi({ rates: personRates });
    renderRoute("/time/settings");

    const ada = (await screen.findByText("Ada Lovelace")).closest("[data-rates]") as HTMLElement;
    const rows = within(ada).getAllByRole("row").slice(1);
    expect(rows[0]).toHaveTextContent("Jul 1, 2026");
    expect(rows[0]).toHaveTextContent("1,200.00");
    expect(rows[1]).toHaveTextContent("Jan 1, 2026");
    expect(rows[1]).toHaveTextContent("700.00");
  });

  it("adds a rate for a user the directory search finds, hours or none", async () => {
    const fetchMock = stubTimeApi({ rates: personRates, assignableUsers: assignableUsers });
    renderRoute("/time/settings");

    await screen.findByText("Ada Lovelace");
    await userEvent.click(screen.getByRole("button", { name: "Add rate" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a rate card" });

    // A new hire with nothing logged is not in the people overview at all; the
    // search reaches the directory, and the term goes to the server.
    await userEvent.type(within(dialog).getByRole("combobox", { name: "Person" }), "Grace");
    await waitFor(() => expect(searchesFor(fetchMock)).toContain("Grace"));
    await userEvent.click(await screen.findByRole("option", { name: "Grace Hopper" }));
    await typeDate(within(dialog).getByRole("textbox", { name: "Valid from" }), "Oct 1, 2026");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Bill rate" }), "1450");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/time/rates",
        body: {
          userId: "22222222-2222-2222-2222-222222222222",
          validFrom: "2026-10-01",
          billRate: 1450,
          costRate: null,
          currency: "NOK",
        },
      }),
    );
  });

  it("refuses a card with neither rate, or a rate of zero, and puts the server's refusal on its field", async () => {
    stubTimeApi({
      rates: personRates,
      assignableUsers: assignableUsers,
      write: () => problemResponse(400, "Invalid rate", { validFrom: ["That day already has a rate card"] }),
    });
    renderRoute("/time/settings");

    await screen.findByText("Ada Lovelace");
    await userEvent.click(screen.getByRole("button", { name: "Add rate" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a rate card" });

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Person" }));
    await userEvent.click(await screen.findByRole("option", { name: "Ada Lovelace" }));
    await typeDate(within(dialog).getByRole("textbox", { name: "Valid from" }), "Jan 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("Give a bill rate, a cost rate, or both")).toBeInTheDocument();

    // Zero is not a rate: §4.3 wants every amount greater than zero.
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Bill rate" }), "0");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("A rate is more than zero")).toBeInTheDocument();

    await userEvent.clear(within(dialog).getByRole("textbox", { name: "Bill rate" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Cost rate" }), "800");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText("That day already has a rate card")).toBeInTheDocument();
  });

  it("keeps the person named when a card is started from their own row", async () => {
    // The search answers nobody — a directory that would not have this person
    // on its first page — so only the prefill can name them.
    const fetchMock = stubTimeApi({ rates: personRates, assignableUsers: [] });
    renderRoute("/time/settings");

    await screen.findByText("Grace Hopper");
    await userEvent.click(screen.getByRole("button", { name: "Add a rate for Grace Hopper" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a rate card" });
    expect(within(dialog).getByRole("combobox", { name: "Person" })).toHaveValue("Grace Hopper");

    await typeDate(within(dialog).getByRole("textbox", { name: "Valid from" }), "Oct 1, 2026");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Cost rate" }), "850");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST").body).toMatchObject({
        userId: "22222222-2222-2222-2222-222222222222",
        validFrom: "2026-10-01",
        costRate: 850,
      }),
    );
  });

  it("keeps the person fixed while an existing card is edited", async () => {
    stubTimeApi({ rates: personRates, assignableUsers: assignableUsers });
    renderRoute("/time/settings");

    const grace = (await screen.findByText("Grace Hopper")).closest("[data-rates]") as HTMLElement;
    await userEvent.click(within(grace).getByRole("button", { name: "Edit the rate" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit the rate card" });

    const person = within(dialog).getByRole("textbox", { name: "Person" });
    expect(person).toHaveValue("Grace Hopper");
    expect(person).toHaveAttribute("readonly");
  });

  it("deletes a rate card once the caller confirms", async () => {
    const fetchMock = stubTimeApi({ rates: personRates });
    renderRoute("/time/settings");

    const grace = (await screen.findByText("Grace Hopper")).closest("[data-rates]") as HTMLElement;
    await userEvent.click(within(grace).getByRole("button", { name: "Delete the rate" }));
    await confirm("Delete the rate?", "Delete");

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe("/api/v1/time/rates/43");
      expect(init?.method).toBe("DELETE");
    });
  });
});

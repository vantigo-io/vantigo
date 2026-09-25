import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { approvalGroups, ME, submittedEntry, WEEK } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";

/** The card for one person's week, found by the name the queue groups it under. */
const groupCard = async (person: string): Promise<HTMLElement> =>
  (await screen.findByText(person)).closest("[data-group]") as HTMLElement;

const open = async (person: string): Promise<HTMLElement> => {
  const card = await groupCard(person);
  await userEvent.click(within(card).getByRole("button", { name: "Show the entries" }));
  return card;
};

describe("ApprovalsPage", () => {
  it("shows an entry's work type and bills its hours at the multiplied rate, exactly", async () => {
    stubTimeApi({
      approvals: [
        {
          userId: ME,
          displayName: "Ada Lovelace",
          weekStart: WEEK,
          hours: 1.5,
          entries: [
            submittedEntry({
              id: 720,
              hours: 1.5,
              note: "Night shift",
              workType: { id: 6001, name: "Overtid 50 %" },
              billing: { billRate: 333.33, currency: "NOK", multiplierPercent: 150, effectiveRate: 500 },
            }),
          ],
        },
      ],
    });
    renderRoute("/time/approvals");

    const ada = await open("Ada Lovelace");
    const row = (await within(ada).findByText("Night shift")).closest("tr") as HTMLElement;
    expect(within(row).getByTestId("work-type-badge")).toHaveTextContent("Overtid 50 %");
    // 333.33 × 1.5 h × 150 % is 749.99 — not 1.5 × the 500.00 an hour shows.
    expect(row).toHaveTextContent("749.99");
    expect(within(row).getByTestId("rate-line")).toHaveTextContent("333.33 × 150 % = 500.00");
  });

  it("tells apart the checkboxes of ordinary hours and overtime on one line and day", async () => {
    stubTimeApi({
      approvals: [
        {
          userId: ME,
          displayName: "Ada Lovelace",
          weekStart: WEEK,
          hours: 9.5,
          entries: [
            submittedEntry({ id: 721, hours: 7.5, note: "Day shift" }),
            submittedEntry({ id: 722, hours: 2, note: "Evening", workType: { id: 6001, name: "Overtid 50 %" } }),
          ],
        },
      ],
    });
    renderRoute("/time/approvals");

    const ada = await open("Ada Lovelace");
    const ordinary = (await within(ada).findByText("Day shift")).closest("tr") as HTMLElement;
    const overtime = within(ada).getByText("Evening").closest("tr") as HTMLElement;
    const ordinaryName = within(ordinary).getByRole("checkbox").getAttribute("aria-label");
    expect(ordinaryName).toMatch(/^Select KVEM1000 › PM on /);
    expect(within(overtime).getByRole("checkbox")).toHaveAccessibleName(
      ordinaryName?.replace("KVEM1000 › PM on", "KVEM1000 › PM as Overtid 50 % on"),
    );
  });

  it("lists a group per person and week, and its entries once the group is opened", async () => {
    stubTimeApi({ approvals: approvalGroups });
    renderRoute("/time/approvals");

    const ada = await groupCard("Ada Lovelace");
    expect(ada).toHaveTextContent("9.5 h");
    expect(within(ada).queryByText("Kickoff")).not.toBeInTheDocument();

    await open("Ada Lovelace");
    const kickoff = (await within(ada).findByText("Kickoff")).closest("tr") as HTMLElement;
    expect(kickoff).toHaveTextContent("KVEM1000 › PM");
    expect(kickoff).toHaveTextContent("7.5");
    // 7.5 hours at the bill rate the entry snapshotted.
    expect(kickoff).toHaveTextContent("9,000.00");

    const task = within(ada).getByText("KVEM1000 › DEV › Skriv spesifikasjonen").closest("tr") as HTMLElement;
    expect(task).toHaveTextContent("Not billable");
  });

  it("approves one entry on its own", async () => {
    const fetchMock = stubTimeApi({ approvals: approvalGroups });
    renderRoute("/time/approvals");

    const ada = await open("Ada Lovelace");
    const kickoff = (await within(ada).findByText("Kickoff")).closest("tr") as HTMLElement;
    await userEvent.click(within(kickoff).getByRole("button", { name: "Approve" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/approve", body: { ids: [701] } }),
    );
    expect(await screen.findByText("Time approved")).toBeInTheDocument();
  });

  it("rejects a whole group with a reason", async () => {
    const fetchMock = stubTimeApi({ approvals: approvalGroups });
    renderRoute("/time/approvals");

    const ada = await groupCard("Ada Lovelace");
    await userEvent.click(within(ada).getByRole("button", { name: "Reject" }));
    const dialog = await screen.findByRole("dialog", { name: "Reject the time?" });

    await userEvent.click(within(dialog).getByRole("button", { name: "Reject" }));
    expect(await within(dialog).findByText("Write why the time is rejected")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Log the hours per task");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reject" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/time/entries/reject",
        body: { ids: [701, 702], reason: "Log the hours per task" },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("approves a batch picked across two groups", async () => {
    const fetchMock = stubTimeApi({ approvals: approvalGroups });
    renderRoute("/time/approvals");

    const grace = await groupCard("Grace Hopper");
    await userEvent.click(within(grace).getByRole("checkbox", { name: "Select Grace Hopper's week" }));

    const ada = await open("Ada Lovelace");
    const kickoff = (await within(ada).findByText("Kickoff")).closest("tr") as HTMLElement;
    await userEvent.click(within(kickoff).getByRole("checkbox"));

    await userEvent.click(screen.getByRole("button", { name: "Approve 2 selected" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/approve", body: { ids: [711, 701] } }),
    );
  });

  it("drops an entry from the selection once the queue no longer holds it", async () => {
    let approved = false;
    stubTimeApi({
      approvals: () =>
        approved
          ? approvalGroups.map((group) => ({ ...group, entries: group.entries.filter((e) => e.id !== 701) }))
          : approvalGroups,
      write: () => {
        approved = true;
        return undefined;
      },
    });
    renderRoute("/time/approvals");

    const grace = await groupCard("Grace Hopper");
    await userEvent.click(within(grace).getByRole("checkbox", { name: "Select Grace Hopper's week" }));
    const ada = await open("Ada Lovelace");
    const kickoff = (await within(ada).findByText("Kickoff")).closest("tr") as HTMLElement;
    await userEvent.click(within(kickoff).getByRole("checkbox"));
    expect(screen.getByRole("button", { name: "Approve 2 selected" })).toBeInTheDocument();

    await userEvent.click(within(kickoff).getByRole("button", { name: "Approve" }));
    expect(await screen.findByRole("button", { name: "Approve 1 selected" })).toBeInTheDocument();
  });

  it("shows every refused id when the batch is turned down as a whole", async () => {
    stubTimeApi({
      approvals: approvalGroups,
      write: () =>
        problemResponse(400, "Invalid approval", {
          ids: ["Entry 701 is not submitted", "Entry 702 is dated before 2026-09-15, the lock date"],
        }),
    });
    renderRoute("/time/approvals");

    const ada = await groupCard("Ada Lovelace");
    await userEvent.click(within(ada).getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("Entry 701 is not submitted")).toBeInTheDocument();
    expect(screen.getByText("Entry 702 is dated before 2026-09-15, the lock date")).toBeInTheDocument();
  });

  it("offers no approval on an entry the caller may not approve", async () => {
    const [grace] = approvalGroups;
    stubTimeApi({
      approvals: [
        {
          ...grace,
          entries: grace.entries.map((entry) => ({
            ...entry,
            capabilities: { ...entry.capabilities, canApprove: false },
          })),
        },
      ],
    });
    renderRoute("/time/approvals");

    const card = await groupCard("Grace Hopper");
    expect(within(card).queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Reject" })).not.toBeInTheDocument();

    await open("Grace Hopper");
    const row = (await within(card).findByText("Hardware bring-up")).closest("tr") as HTMLElement;
    expect(within(row).queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
  });

  it("takes only the approvable entries into a batch picked by the group", async () => {
    const [, ada] = approvalGroups;
    const fetchMock = stubTimeApi({
      approvals: [
        {
          ...ada,
          entries: ada.entries.map((entry) =>
            entry.id === 702 ? { ...entry, capabilities: { ...entry.capabilities, canApprove: false } } : entry,
          ),
        },
      ],
    });
    renderRoute("/time/approvals");

    const card = await groupCard("Ada Lovelace");
    await userEvent.click(within(card).getByRole("checkbox", { name: "Select Ada Lovelace's week" }));
    // The group holds two entries; only one of them is the caller's to approve.
    expect(screen.getByRole("button", { name: "Approve 1 selected" })).toBeInTheDocument();

    await open("Ada Lovelace");
    const task = within(card).getByText("KVEM1000 › DEV › Skriv spesifikasjonen").closest("tr") as HTMLElement;
    expect(within(task).queryByRole("checkbox")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Approve 1 selected" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/entries/approve", body: { ids: [701] } }),
    );
  });

  it("offers no group checkbox when nothing in the week is the caller's to approve", async () => {
    const [grace] = approvalGroups;
    stubTimeApi({
      approvals: [
        {
          ...grace,
          entries: grace.entries.map((entry) => ({
            ...entry,
            capabilities: { ...entry.capabilities, canApprove: false },
          })),
        },
      ],
    });
    renderRoute("/time/approvals");

    const card = await groupCard("Grace Hopper");
    expect(within(card).queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("says one entry in the singular, and tells the expand control's state", async () => {
    stubTimeApi({ approvals: approvalGroups });
    renderRoute("/time/approvals");

    const grace = await groupCard("Grace Hopper");
    const expand = within(grace).getByRole("button", { name: "Show the entries" });
    expect(expand).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(expand);
    const collapse = within(grace).getByRole("button", { name: "Hide the entries" });
    expect(collapse).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(collapse);

    await userEvent.click(within(grace).getByRole("button", { name: "Approve" }));
    expect(await screen.findByText("1 entry")).toBeInTheDocument();
  });

  it("forgets the selection when the queue turns to another page", async () => {
    stubTimeApi({ approvals: approvalGroups });
    const { router } = renderRoute("/time/approvals");

    const grace = await groupCard("Grace Hopper");
    await userEvent.click(within(grace).getByRole("checkbox", { name: "Select Grace Hopper's week" }));
    expect(screen.getByRole("button", { name: "Approve 1 selected" })).toBeInTheDocument();

    await router.navigate({ to: "/time/approvals", search: { page: 2 } });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Approve 1 selected" })).not.toBeInTheDocument());
  });

  it("says so plainly when the caller approves nobody's time", async () => {
    stubTimeApi({ approvals: problemResponse(403, "Forbidden") });
    renderRoute("/time/approvals");

    expect(await screen.findByText("You approve nobody's time")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows an empty queue as nothing waiting", async () => {
    stubTimeApi({ approvals: [] });
    renderRoute("/time/approvals");

    expect(await screen.findByText("Nothing waiting for approval")).toBeInTheDocument();
  });
});

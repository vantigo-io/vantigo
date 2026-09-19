import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { approvalGroups } from "../test/fixtures";
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

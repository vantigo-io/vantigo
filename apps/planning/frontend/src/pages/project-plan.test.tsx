import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { allocation, allocations } from "../test/fixtures";
import { retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { planningProblem, stubPlanningApi } from "../test/server";

const rowOf = async (name: string) =>
  (await within(await screen.findByRole("table", { name: "Allocations" })).findByText(name)).closest(
    "tr",
  ) as HTMLElement;

describe("ProjectPlanPage — a project's plan", () => {
  it("names the project and lists its rows with the hours each week", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/projects/100");

    expect(await screen.findByRole("heading", { name: /P-100 Harbour app/ })).toHaveTextContent("Active");
    expect(screen.getByText(/Fjord AS/)).toBeInTheDocument();
    const ada = await rowOf("Ada Lovelace");
    expect(ada).toHaveTextContent("Frontend developer");
    expect(ada).toHaveTextContent("50%");
    expect(ada).toHaveTextContent("Confirmed");
    // The note is the planners' (S5): a viewer is never sent it.
    expect(ada).not.toHaveTextContent("Wants the native part");
    expect(await rowOf("Linus Torvalds")).toHaveTextContent("20 h/week");
    // 18.75 + 20 hours in the first week.
    const weekly = screen.getByRole("table", { name: "Hours per week" });
    expect(within(weekly).getByRole("rowheader", { name: "Booked" }).closest("tr")).toHaveTextContent("38.8 h");
    expect(screen.queryByRole("button", { name: "New allocation" })).not.toBeInTheDocument();
  });

  it("shows a person Staff no longer answers as Erased person", async () => {
    stubPlanningApi({
      role: "view",
      allocations: [allocation({ id: 600, personId: 99, projectId: 100 })],
    });
    renderRoute("/planning/projects/100");
    expect(await rowOf("Erased person")).toHaveTextContent("50%");
  });

  it("changes a row with the revision it read", async () => {
    const fetchMock = stubPlanningApi({ allocations: allocations().map((row) => ({ ...row, revision: 4 })) });
    renderRoute("/planning/projects/100");

    await userEvent.click(
      within(await rowOf("Ada Lovelace")).getByRole("button", { name: "Actions for Ada Lovelace" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const sheet = await screen.findByRole("dialog", { name: "Change allocation" });
    expect(within(sheet).getByRole("textbox", { name: "Note" })).toHaveValue("Wants the native part");
    await retype(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "80");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/allocations\/501$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/allocations\/501$/);
    expect(put.ifMatch).toBe("4");
    expect(put.body).toMatchObject({ personId: 1, projectId: 100, load: 80, loadUnit: "percent", status: "confirmed" });
    expect(await screen.findByText("The allocation is saved")).toBeInTheDocument();
    expect(fetchMock.state.allocations.find((row) => row.id === 501)?.revision).toBe(5);
  });

  it("reloads and closes the sheet when somebody else saved the row first", async () => {
    const fetchMock = stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/allocations/501"
          ? planningProblem(409, "revision_conflict", { title: "Revision conflict" })
          : undefined,
    });
    renderRoute("/planning/projects/100");

    await userEvent.click(
      within(await rowOf("Ada Lovelace")).getByRole("button", { name: "Actions for Ada Lovelace" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const sheet = await screen.findByRole("dialog", { name: "Change allocation" });
    const reads = sent(fetchMock, "GET", /\/projects\/100\/plan/).length;
    await retype(within(sheet).getByRole("textbox", { name: "Percent of the position" }), "80");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.getByText("Check what is there now and make your change again.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Change allocation" })).not.toBeInTheDocument());
    expect(sent(fetchMock, "GET", /\/projects\/100\/plan/).length).toBeGreaterThan(reads);
    expect(fetchMock.state.allocations.find((row) => row.id === 501)?.load).toBe(50);
  });

  it("names a person as A colleague when Staff answers the caller nobody", async () => {
    stubPlanningApi({
      role: "view",
      meta: { workforceVisible: false },
      allocations: [allocation({ id: 600, personId: 99, projectId: 100 })],
    });
    renderRoute("/planning/projects/100");
    expect(await rowOf("A colleague")).toHaveTextContent("50%");
    expect(screen.queryByText("Erased person")).not.toBeInTheDocument();
  });

  it("confirms a tentative row and deletes one after asking", async () => {
    const fetchMock = stubPlanningApi({
      allocations: [
        allocation({ id: 700, personId: 2, projectId: 100, status: "tentative", confirmedAt: undefined }),
        allocation({ id: 701, personId: 3, projectId: 100 }),
      ],
    });
    renderRoute("/planning/projects/100");

    await userEvent.click(
      within(await rowOf("Grace Hopper")).getByRole("button", { name: "Actions for Grace Hopper" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Confirm" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/allocations\/700\/confirm$/)).toHaveLength(1));
    await waitFor(async () => expect(await rowOf("Grace Hopper")).toHaveTextContent("Confirmed"));

    await userEvent.click(
      within(await rowOf("Linus Torvalds")).getByRole("button", { name: "Actions for Linus Torvalds" }),
    );
    // A confirmed row offers no Confirm.
    expect(screen.queryByRole("menuitem", { name: "Confirm" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const ask = await screen.findByRole("dialog", { name: "Delete this allocation?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/allocations\/701$/)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByText("Linus Torvalds")).not.toBeInTheDocument());
  });

  it("confirms from the sheet too", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/projects/200");
    await userEvent.click(
      within(await rowOf("Grace Hopper")).getByRole("button", { name: "Actions for Grace Hopper" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const sheet = await screen.findByRole("dialog", { name: "Change allocation" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/allocations\/502\/confirm$/)).toHaveLength(1));
    expect(await screen.findByText("The allocation is confirmed")).toBeInTheDocument();
  });
});

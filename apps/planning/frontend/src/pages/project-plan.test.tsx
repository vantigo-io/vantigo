import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { allocation, allocations, week, weekEnd } from "../test/fixtures";
import { choose, retype, sent } from "../test/helpers";
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

describe("ProjectPlan — the requests and the demand", () => {
  it("adds the open requests' hours a week as demand, a partial first week by its weekdays", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/projects/200");
    const weekly = await screen.findByRole("table", { name: "Hours per week" });
    const demand = within(weekly).getByRole("rowheader", { name: "Demand" }).closest("tr") as HTMLElement;
    const cells = within(demand).getAllByRole("cell");
    // From Wednesday: 3 of 5 days of 30 h, then whole weeks.
    expect(cells[0]).toHaveTextContent("18 h");
    expect(cells[1]).toHaveTextContent("30 h");
    expect(screen.getByRole("list", { name: "Requests of P-200 Tender bid" })).toBeInTheDocument();
    const requests = screen.getByRole("table", { name: "Requests" });
    expect(within(requests).getByText("Backend developer").closest("tr")).toHaveTextContent("Open");
    expect(screen.queryByRole("button", { name: "New request" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Candidates for Backend developer" })).not.toBeInTheDocument();
  });

  it("shows a view holder no request note", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/projects/200");
    const requests = await screen.findByRole("table", { name: "Requests" });
    expect(within(requests).getByText("Backend developer").closest("tr")).not.toHaveTextContent(
      "Bergen office preferred",
    );
  });

  it("shows a planner the request's note", async () => {
    stubPlanningApi();
    renderRoute("/planning/projects/200");
    const requests = await screen.findByRole("table", { name: "Requests" });
    expect(within(requests).getByText("Backend developer").closest("tr")).toHaveTextContent("Bergen office preferred");
  });

  it("opens a filled request read-only from its menu", async () => {
    stubPlanningApi();
    renderRoute("/planning/projects/100");
    await userEvent.click(await screen.findByRole("button", { name: "Actions for Tester" }));
    expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Open" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    expect(await within(sheet).findByText(/A filled or withdrawn request takes no change/)).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("lists a filled request with its person and no demand", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/projects/100");
    const requests = await screen.findByRole("table", { name: "Requests" });
    expect(within(requests).getByText("Tester").closest("tr")).toHaveTextContent("Filled by Grace Hopper");
    expect(within(requests).queryByText("Designer")).not.toBeInTheDocument();
    const weekly = screen.getByRole("table", { name: "Hours per week" });
    expect(within(weekly).getByRole("rowheader", { name: "Demand" }).closest("tr")).toHaveTextContent("0 h");
  });

  it("files a new request on the project over the plan's weeks", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/projects/200");
    await userEvent.click(await screen.findByRole("button", { name: "New request" }));
    const sheet = await screen.findByRole("dialog", { name: "New request" });
    expect(within(sheet).getByRole("combobox", { name: "Project" })).toHaveValue("P-200 Tender bid");

    await userEvent.type(within(sheet).getByRole("textbox", { name: "Title" }), "Data engineer");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Hours per week" }), "37.5");
    await userEvent.click(within(sheet).getByRole("button", { name: "Add requirement" }));
    await choose(sheet, "Requirement 1", "React");
    await choose(sheet, "Lowest level", "experienced");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("The request is saved")).toBeInTheDocument();
    const [post] = sent(fetchMock, "POST", /\/planning\/requests$/);
    expect(post.body).toEqual({
      projectId: 200,
      title: "Data engineer",
      fromDate: week(0),
      toDate: weekEnd(11),
      hoursPerWeek: 37.5,
      requirements: [{ competenceId: 40, minLevel: 3 }],
    });
  });

  it("asks for a competence on a requirement row left empty", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/projects/200");
    await userEvent.click(await screen.findByRole("button", { name: "New request" }));
    const sheet = await screen.findByRole("dialog", { name: "New request" });
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Title" }), "Data engineer");
    await userEvent.type(within(sheet).getByRole("textbox", { name: "Hours per week" }), "10");
    await userEvent.click(within(sheet).getByRole("button", { name: "Add requirement" }));
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("Pick a competence")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/planning\/requests$/)).toHaveLength(0);
  });

  it("opens a request's candidates and withdraws one from the plan", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/projects/200");
    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Backend developer" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Backend developer" });
    expect(await within(panel).findByRole("listitem", { name: "Ada Lovelace" })).toBeInTheDocument();
    await userEvent.click(within(panel).getByRole("button", { name: "Close" }));

    await userEvent.click(screen.getByRole("button", { name: "Actions for Backend developer" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Withdraw" }));
    const ask = await screen.findByRole("dialog", { name: "Withdraw this request?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Withdraw" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/requests\/801\/withdraw$/)).toHaveLength(1));
  });

  it("renders on the host's project tab without the page's header", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/projects/100/planning");
    expect(await screen.findByRole("table", { name: "Hours per week" })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Requests" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /P-100 Harbour app/ })).not.toBeInTheDocument();
  });
});

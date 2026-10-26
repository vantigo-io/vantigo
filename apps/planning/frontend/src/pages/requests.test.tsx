import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { request, week } from "../test/fixtures";
import { choose, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type PlanningStub, planningProblem, stubPlanningApi } from "../test/server";

const rowOf = async (title: string) =>
  (await within(await screen.findByRole("table", { name: "Requests" })).findByText(title)).closest("tr") as HTMLElement;

/** Request 801 as filled meanwhile by somebody else, so a reload reads it so. */
const fillMeanwhile = (fetchMock: PlanningStub) => {
  fetchMock.state.requests = fetchMock.state.requests.map((row) =>
    row.id === 801 ? { ...row, status: "filled", revision: row.revision + 1 } : row,
  );
};

describe("RequestsPage — the requests", () => {
  it("lists every request with its project, status and requirements, without the writes for a viewer", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/requests?status=all");

    const backend = await rowOf("Backend developer");
    expect(backend).toHaveTextContent("P-200 Tender bid");
    expect(backend).toHaveTextContent("30 h/week");
    expect(backend).toHaveTextContent("Open");
    expect(backend).toHaveTextContent("React (at least independent), Safety card");
    const tester = await rowOf("Tester");
    expect(tester).toHaveTextContent("Filled");
    expect(tester).toHaveTextContent("Filled by Grace Hopper");
    expect(await rowOf("Designer")).toHaveTextContent("Withdrawn");
    expect(screen.queryByRole("button", { name: "New request" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Candidates for/ })).not.toBeInTheDocument();
  });

  it("shows the open requests unless another status is picked, and every status on All statuses", async () => {
    const fetchMock = stubPlanningApi({ role: "view" });
    const { router } = renderRoute("/planning/requests");
    await rowOf("Backend developer");
    const table = screen.getByRole("table", { name: "Requests" });
    expect(within(table).queryByText("Tester")).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveValue("Open");
    expect(sent(fetchMock, "GET", /\/planning\/requests\?/).at(-1)?.url).toContain("status=open");

    await choose(document.body, "Status", "Filled");
    await waitFor(() => expect(router.state.location.search).toMatchObject({ status: "filled" }));
    await rowOf("Tester");
    expect(within(screen.getByRole("table", { name: "Requests" })).queryByText("Backend developer")).toBeNull();

    await choose(document.body, "Status", "All statuses");
    await waitFor(() => expect(router.state.location.search).toMatchObject({ status: "all" }));
    await rowOf("Designer");
    expect(sent(fetchMock, "GET", /\/planning\/requests(\?|$)/).at(-1)?.url).not.toContain("status=");
  });

  it("says when the list stops at the first 500 requests", async () => {
    stubPlanningApi({
      role: "view",
      requests: Array.from({ length: 500 }, (_, index) =>
        request({ id: 1000 + index, projectId: 200, title: `Request ${index + 1}` }),
      ),
    });
    renderRoute("/planning/requests");
    expect(await screen.findByText("Showing the first 500 requests")).toBeInTheDocument();
  });

  it("says nothing of a limit below 500 requests", async () => {
    stubPlanningApi({ role: "view" });
    renderRoute("/planning/requests");
    await rowOf("Backend developer");
    expect(screen.queryByText(/Showing the first/)).not.toBeInTheDocument();
  });

  it("opens the sheet on arrival for the spotlight's New request, and drops new from the URL on close", async () => {
    stubPlanningApi();
    const { router } = renderRoute("/planning/requests?new=1");
    const sheet = await screen.findByRole("dialog", { name: "New request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("new"));
  });

  it("changes an open request with the revision it read", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/requests");

    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    expect(within(sheet).getByRole("textbox", { name: "Note" })).toHaveValue("Bergen office preferred");
    expect(within(sheet).getByRole("combobox", { name: "Requirement 1" })).toHaveValue("React");
    await retype(within(sheet).getByRole("textbox", { name: "Hours per week" }), "20");
    await userEvent.click(within(sheet).getByRole("button", { name: "Remove requirement 2" }));
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/requests\/801$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/requests\/801$/);
    expect(put.ifMatch).toBe("1");
    expect(put.body).toMatchObject({
      projectId: 200,
      title: "Backend developer",
      hoursPerWeek: 20,
      note: "Bergen office preferred",
      requirements: [{ competenceId: 40, minLevel: 2 }],
    });
    expect(await screen.findByText("The request is saved")).toBeInTheDocument();
  });

  it("says a request filled meanwhile takes no change, and turns the sheet read-only without closing it", async () => {
    const fetchMock: PlanningStub = stubPlanningApi({
      write: (method, path) => {
        if (method !== "PUT" || path !== "/requests/801") return undefined;
        fillMeanwhile(fetchMock);
        return planningProblem(409, "request_not_open", { title: "Request not open" });
      },
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(
      await within(sheet).findByText(
        /The request is filled or withdrawn: it takes no fill, no change and no withdrawal/,
      ),
    ).toBeInTheDocument();
    await waitFor(() => expect(within(sheet).queryByRole("button", { name: "Save" })).not.toBeInTheDocument());
    expect(within(sheet).getByText(/A filled or withdrawn request takes no change/)).toBeInTheDocument();
    expect(within(sheet).getByRole("textbox", { name: "Title" })).toBeDisabled();
    expect(screen.getByRole("dialog", { name: "Change request" })).toBeInTheDocument();
  });

  it("shows a filled request without letting it be changed", async () => {
    stubPlanningApi();
    renderRoute("/planning/requests?status=all");
    await userEvent.click(await screen.findByRole("button", { name: "Change Tester" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    expect(within(sheet).getByText(/A filled or withdrawn request takes no change/)).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(within(sheet).getByRole("textbox", { name: "Title" })).toBeDisabled();
  });

  it("puts the server's field errors on their fields, a requirement's on its row", async () => {
    stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/requests/801"
          ? planningProblem(400, undefined, {
              title: "Invalid request",
              errors: { title: ["Too long"], "requirements[0].minLevel": ["React takes no level"] },
            })
          : undefined,
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("Too long")).toBeInTheDocument();
    expect(within(sheet).getByText("React takes no level")).toBeInTheDocument();
    expect(within(sheet).queryByText("Could not save")).not.toBeInTheDocument();
  });

  it("puts an error on a requirement's competence on that requirement's row", async () => {
    stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/requests/801"
          ? planningProblem(400, undefined, {
              title: "Invalid request",
              errors: { "requirements[1].competenceId": ["No such competence"] },
            })
          : undefined,
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    const second = within(sheet).getByRole("combobox", { name: "Requirement 2" });
    await waitFor(() => expect(second).toHaveAccessibleDescription(/No such competence/));
    expect(within(sheet).getByRole("combobox", { name: "Requirement 1" })).not.toHaveAccessibleDescription(
      /No such competence/,
    );
  });

  it("says a field error the sheet has no field for above the buttons", async () => {
    stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/requests/801"
          ? planningProblem(400, undefined, {
              title: "Invalid request",
              errors: { "requirements[7].competenceId": ["No such competence"], revision: ["Stale"] },
            })
          : undefined,
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("Could not save")).toBeInTheDocument();
    expect(within(sheet).getByText("Invalid request")).toBeInTheDocument();
  });

  it("words a window out of bounds on the period", async () => {
    stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/requests/801"
          ? planningProblem(400, "window_out_of_bounds", {
              title: "Invalid request",
              errors: { toDate: ["Out of bounds"] },
            })
          : undefined,
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Change Backend developer" }));
    const sheet = await screen.findByRole("dialog", { name: "Change request" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText(/These weeks are outside what Planning shows/)).toBeInTheDocument();
    expect(within(sheet).queryByText("Out of bounds")).not.toBeInTheDocument();
  });

  it("withdraws an open request after asking", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/requests?status=all");
    const backend = await rowOf("Backend developer");
    expect(within(await rowOf("Tester")).queryByRole("button", { name: /^Withdraw/ })).not.toBeInTheDocument();

    await userEvent.click(within(backend).getByRole("button", { name: "Withdraw Backend developer" }));
    const ask = await screen.findByRole("dialog", { name: "Withdraw this request?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Withdraw" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/requests\/801\/withdraw$/)).toHaveLength(1));
    expect(await screen.findByText("The request is withdrawn")).toBeInTheDocument();
    expect(fetchMock.state.requests.find((row) => row.id === 801)?.status).toBe("withdrawn");
  });
});

describe("CandidatesPanel — filling a request", () => {
  it("shows each candidate's availability, fit, flags and current project, and the partial ones on asking", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Backend developer" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Backend developer" });

    const ada = await within(panel).findByRole("listitem", { name: "Ada Lovelace" });
    expect(ada).toHaveTextContent("Has the time");
    expect(ada).toHaveTextContent("Fit 100/100");
    expect(ada).toHaveTextContent("Covers it");
    expect(ada).toHaveTextContent("260 h free of 222 h needed");
    const linus = within(panel).getByRole("listitem", { name: "Linus Torvalds" });
    expect(linus).toHaveTextContent("Partly free");
    expect(linus).toHaveTextContent(/Certificate expires .+ \(Safety card\)/);
    expect(linus).toHaveTextContent("Lowered by: Weekdays, Location");
    expect(linus).toHaveTextContent("On P-100 Harbour app for 90 days");
    expect(within(panel).queryByRole("listitem", { name: "Grace Hopper" })).not.toBeInTheDocument();

    await userEvent.click(within(panel).getByRole("switch", { name: /Show partial matches/ }));
    const grace = await within(panel).findByRole("listitem", { name: "Grace Hopper" });
    expect(grace).toHaveTextContent("Missing: React");
    expect(sent(fetchMock, "GET", /\/requests\/801\/candidates\?includePartial=true$/)).toHaveLength(1);
  });

  it("fills as tentative on a planned project unless confirmed is picked, and says the fill's warnings", async () => {
    const fetchMock = stubPlanningApi({ warnings: [{ code: "overbooked", weeks: [week(1)] }] });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Backend developer" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Backend developer" });
    await within(panel).findByRole("listitem", { name: "Ada Lovelace" });
    expect(within(panel).getByRole("radio", { name: "Tentative" })).toBeChecked();

    await userEvent.click(within(panel).getByText("Confirmed"));
    await userEvent.click(within(panel).getByRole("button", { name: "Fill with Ada Lovelace" }));

    expect(await within(panel).findByText("Filled — but look at this")).toBeInTheDocument();
    expect(within(panel).getByText(/More is booked than the person's capacity/)).toBeInTheDocument();
    const [fill] = sent(fetchMock, "POST", /\/requests\/801\/fill$/);
    expect(fill.body).toEqual({ personId: 1, status: "confirmed" });
    expect(fetchMock.state.requests.find((row) => row.id === 801)?.status).toBe("filled");
    expect(fetchMock.state.allocations.at(-1)).toMatchObject({ requestId: 801, load: 30, loadUnit: "hours_per_week" });
  });

  it("takes the Fill buttons away once a fill is refused as no longer open", async () => {
    const fetchMock: PlanningStub = stubPlanningApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/requests/801/fill") return undefined;
        fillMeanwhile(fetchMock);
        return planningProblem(409, "request_not_open", { title: "Request not open" });
      },
    });
    renderRoute("/planning/requests");
    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Backend developer" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Backend developer" });
    await userEvent.click(await within(panel).findByRole("button", { name: "Fill with Ada Lovelace" }));
    expect(await within(panel).findByText("Could not fill the request")).toBeInTheDocument();
    expect(await within(panel).findByText(/This request is no longer open/)).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: /^Fill with/ })).not.toBeInTheDocument();
    expect(within(panel).getByRole("listitem", { name: "Ada Lovelace" })).toBeInTheDocument();
  });

  it("says a request no longer open, and offers no fill on a filled one", async () => {
    stubPlanningApi({
      write: (method, path) =>
        method === "POST" && path === "/requests/801/fill"
          ? planningProblem(409, "request_not_open", { title: "Request not open" })
          : undefined,
    });
    renderRoute("/planning/requests?status=all");
    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Backend developer" }));
    const panel = await screen.findByRole("dialog", { name: "Candidates — Backend developer" });
    await userEvent.click(await within(panel).findByRole("button", { name: "Fill with Ada Lovelace" }));
    expect(await within(panel).findByText("Could not fill the request")).toBeInTheDocument();
    expect(within(panel).getByText(/The request is filled or withdrawn/)).toBeInTheDocument();
    await userEvent.click(within(panel).getByRole("button", { name: "Close" }));

    await userEvent.click(await screen.findByRole("button", { name: "Candidates for Tester" }));
    const filled = await screen.findByRole("dialog", { name: "Candidates — Tester" });
    expect(await within(filled).findByText(/This request is no longer open/)).toBeInTheDocument();
    await within(filled).findByRole("listitem", { name: "Ada Lovelace" });
    expect(within(filled).queryByRole("button", { name: /^Fill with/ })).not.toBeInTheDocument();
  });
});

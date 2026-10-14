import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { cases } from "../test/absence-fixtures";
import { region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

describe("My page — My team, shown by the capability", () => {
  it("is a tab of My page for a caller who manages anyone", async () => {
    // Ada manages Grace by her terms (the fake answers managesAnyone so).
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me");

    expect(await screen.findByRole("tab", { name: "My team" })).toBeInTheDocument();
  });

  it("is not there for one who manages nobody, and reads nothing of a team", async () => {
    // Grace manages nobody.
    const fetchMock = stubStaffApi({ role: "access", me: 102 });
    renderRoute("/staff/me?tab=team");

    await screen.findByRole("tab", { name: "Absence" });
    expect(screen.queryByRole("tab", { name: "My team" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/approvals/)).toHaveLength(0);
    expect(sent(fetchMock, "GET", /\/cases/)).toHaveLength(0);
  });
});

describe("My page — My team", () => {
  it("shows my reports' absence this month as the server shaped it", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const month = await region("Absence in October 2026");
    const grace = await within(month).findByRole("list", { name: "Grace Hopper" });
    const rows = within(grace)
      .getAllByRole("listitem")
      .map((item) => item.textContent);
    expect(rows).toEqual([
      "Holiday · Oct 19, 2026 – Oct 23, 2026 · Approved",
      "Leave · Oct 26, 2026 – Oct 27, 2026 · Approved",
    ]);
    expect(month).not.toHaveTextContent("%");
  });

  it("approves a request inline", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const request = await screen.findByRole("article", { name: "Grace Hopper: Holiday, Nov 2, 2026 – Nov 6, 2026" });
    expect(within(request).getByText("5 working days")).toBeInTheDocument();
    expect(within(request).getByText("Family visit")).toBeInTheDocument();
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences\/601\/approve$/)).toHaveLength(1));
    expect(await screen.findByText("Nothing waits for your decision")).toBeInTheDocument();
  });

  it("rejects a request with the reason it asks for", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const request = await screen.findByRole("article", { name: "Grace Hopper: Holiday, Nov 2, 2026 – Nov 6, 2026" });
    await userEvent.click(within(request).getByRole("button", { name: "Reject" }));
    await userEvent.click(within(request).getByRole("button", { name: "Reject request" }));
    expect(await within(request).findByText("Say why the request is rejected")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/reject$/)).toHaveLength(0);

    await retype(within(request).getByRole("textbox", { name: "Reason" }), "Release week");
    await userEvent.click(within(request).getByRole("button", { name: "Reject request" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences\/601\/reject$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/absences\/601\/reject$/)[0].body).toEqual({ note: "Release week" });
  });

  it("says in words that somebody was quicker", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "POST" && path.endsWith("/approve")
          ? staffProblem(409, {
              code: "absence_not_requested",
              title: "Absence not requested",
              detail: "The server's English.",
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=team");

    const request = await screen.findByRole("article", { name: "Grace Hopper: Holiday, Nov 2, 2026 – Nov 6, 2026" });
    await userEvent.click(within(request).getByRole("button", { name: "Approve" }));

    expect(
      await screen.findByText("This request is no longer waiting: somebody decided it, or it was withdrawn."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("shows an open case's milestones and marks one done", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    expect(within(sickCase).getByText("Shorter days for now")).toBeInTheDocument();
    // The end of the employer's period is the rows', never ticked or waived.
    const employerPeriod = within(sickCase).getByRole("group", { name: "End of the employer's period" });
    expect(within(employerPeriod).getByText("Oct 13, 2026")).toBeInTheDocument();
    expect(within(employerPeriod).queryByRole("button")).not.toBeInTheDocument();
    const plan = within(sickCase).getByRole("group", { name: "Follow-up plan" });
    expect(within(plan).getByText("Due")).toBeInTheDocument();
    await userEvent.click(within(plan).getByRole("button", { name: "Mark done" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST", /\/cases\/701\/milestones\/follow_up_plan\/done$/)).toHaveLength(1),
    );
    expect(await within(plan).findByText("Done Oct 11, 2026")).toBeInTheDocument();
  });

  it("waives a milestone with the reason", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    const meeting = within(sickCase).getByRole("group", { name: "First dialogue meeting" });
    await userEvent.click(within(meeting).getByRole("button", { name: "Waive" }));
    const dialog = await screen.findByRole("dialog", { name: "Waive: First dialogue meeting" });
    await retype(within(dialog).getByRole("textbox", { name: "Why it is not needed" }), "Back at work full time");
    await userEvent.click(within(dialog).getByRole("button", { name: "Waive" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST", /\/cases\/701\/milestones\/dialogue_1\/waive$/)).toHaveLength(1),
    );
    expect(sent(fetchMock, "POST", /\/waive$/)[0].body).toEqual({ reason: "Back at work full time" });
    expect(await within(meeting).findByText("Waived")).toBeInTheDocument();
  });

  it("changes the accommodation note with the case's revision", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    await userEvent.click(within(sickCase).getByRole("button", { name: "Change the accommodation note" }));
    const dialog = await screen.findByRole("dialog", { name: "Accommodation note" });
    await retype(within(dialog).getByRole("textbox", { name: "Accommodation note" }), "Four-hour days");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/cases\/701$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/cases\/701$/);
    expect(put.ifMatch).toBe("2");
    expect(put.body).toEqual({ accommodationNote: "Four-hour days" });
    expect(await within(sickCase).findByText("Four-hour days")).toBeInTheDocument();
  });

  it("puts the cursor in the reason when a request is rejected", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const request = await screen.findByRole("article", { name: "Grace Hopper: Holiday, Nov 2, 2026 – Nov 6, 2026" });
    await userEvent.click(within(request).getByRole("button", { name: "Reject" }));
    await waitFor(() => expect(within(request).getByRole("textbox", { name: "Reason" })).toHaveFocus());
  });

  it("closes the accommodation note on what the other writer saved, and never sends it twice", async () => {
    const fetchMock = stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/cases/701"
          ? staffProblem(409, {
              code: "revision_conflict",
              title: "Revision conflict",
              detail: "The case has revision 3; the supplied revision was 2.",
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    await userEvent.click(within(sickCase).getByRole("button", { name: "Change the accommodation note" }));
    const dialog = await screen.findByRole("dialog", { name: "Accommodation note" });
    await retype(within(dialog).getByRole("textbox", { name: "Accommodation note" }), "Four-hour days");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Accommodation note" })).not.toBeInTheDocument());
    expect(sent(fetchMock, "PUT", /\/cases\/701$/)).toHaveLength(1);
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
  });

  it("leaves out a case of somebody who is not my report", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      absence: { cases: [...cases(), { ...cases()[0], id: 702, personId: 104, firstDay: "2026-10-05" }] },
    });
    renderRoute("/staff/me?tab=team");

    const open = await region("Open sick-leave cases");
    await within(open).findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    expect(within(open).getAllByRole("article")).toHaveLength(1);
  });

  it("shows a milestone's note and a waiver's reason only when the case carries them", async () => {
    const [carried] = cases();
    carried.milestones = carried.milestones.map((one) =>
      one.kind === "dialogue_1"
        ? { ...one, waived: true, waivedReason: "Back at work full time" }
        : one.kind === "follow_up_plan"
          ? { ...one, note: "Plan agreed by phone" }
          : one,
    );
    stubStaffApi({ role: "access", me: 101, absence: { cases: [carried] } });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    expect(
      within(within(sickCase).getByRole("group", { name: "First dialogue meeting" })).getByText(
        "Back at work full time",
      ),
    ).toBeInTheDocument();
    expect(
      within(within(sickCase).getByRole("group", { name: "Follow-up plan" })).getByText("Plan agreed by phone"),
    ).toBeInTheDocument();
  });

  it("shows no milestone note or waiver reason the case does not carry", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=team");

    const sickCase = await screen.findByRole("article", { name: "Grace Hopper, from Sep 28, 2026" });
    for (const name of ["Follow-up plan", "First dialogue meeting", "Second dialogue meeting"]) {
      const group = within(sickCase).getByRole("group", { name });
      // The label and the date, nothing else in writing.
      expect(
        within(group)
          .getAllByText(/./, { selector: "p" })
          .map((one) => one.textContent),
      ).toHaveLength(2);
    }
  });
});

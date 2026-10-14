import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  casesAsApprover,
  casesAsPersonalData,
  graceAbsencesAsHr,
  graceAsHr,
  graceAsManager,
  selfCertification,
} from "../test/absence-fixtures";
import { people, person } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

/** One of the person's absences, by its name and its days as the list writes them. */
const row = (name: string) => screen.findByRole("article", { name });

const SUMMER = "Holiday, Jul 6, 2026 – Jul 24, 2026";
const SICK = "Certified sick leave, Sep 28, 2026 – Oct 30, 2026";
const LEAVE = "Leave, Aug 17, 2026 – Aug 21, 2026";
const CHRISTMAS = "Holiday, Dec 21, 2026 – Dec 23, 2026";

describe("Person page — the Absence tab, shown to who may read it", () => {
  it("is a tab for HR, opened from the URL", async () => {
    stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    expect(await screen.findByRole("tab", { name: "Absence", selected: true })).toBeInTheDocument();
    expect(await region("Holiday account 2026")).toBeInTheDocument();
  });

  it("is not there for a colleague, whose holiday-account read is the unknown id's 404", async () => {
    const fetchMock = stubStaffApi({ role: "view", absence: { people: {} } });
    renderRoute("/staff/people/102?tab=absence");

    await screen.findByRole("tab", { name: "Profile", selected: true });
    await waitFor(() => expect(sent(fetchMock, "GET", /people\/102\/holiday-account/)).toHaveLength(1));
    await expect(screen.findByRole("tab", { name: "Absence" }, { timeout: 300 })).rejects.toThrow();
  });
});

describe("Person page — the Absence tab as HR reads it", () => {
  it("shows the holiday account, a year at a time", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const account = await region("Holiday account 2026");
    expect(within(account).getByText("Remaining").nextElementSibling).toHaveTextContent("9");
    await userEvent.click(screen.getByRole("button", { name: "Previous year" }));
    const before = await region("Holiday account 2025");
    expect(within(before).getByText("Remaining").nextElementSibling).toHaveTextContent("3");
    expect(sent(fetchMock, "GET", /people\/102\/holiday-account\?year=2025/)).toHaveLength(1);
  });

  it("lists the rows as the server shaped them: HR-only leave as leave, with nothing to change", async () => {
    stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const summer = await row(SUMMER);
    expect(within(summer).getByText("Approved")).toBeInTheDocument();
    expect(within(summer).getByText("Enjoy")).toBeInTheDocument();
    const leave = await row(LEAVE);
    expect(within(leave).getByText("Approved")).toBeInTheDocument();
    // A row shown as leave carries no type to correct it to and no note.
    expect(within(leave).queryByRole("button")).not.toBeInTheDocument();
    expect(within(await row(CHRISTMAS)).getByText("Christmas")).toBeInTheDocument();
  });

  it("corrects an approved row with the etag it carries, quoted", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(within(await row(SUMMER)).getByRole("button", { name: "Correct" }));
    const sheet = await screen.findByRole("dialog", { name: "Correct absence" });
    await typeDate(within(sheet).getByRole("textbox", { name: "Last day" }), "Jul 17, 2026");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save correction" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /absences\/711\/correct$/)).toHaveLength(1));
    const [correction] = sent(fetchMock, "POST", /absences\/711\/correct$/);
    expect(correction.ifMatch).toBe('"e711"');
    expect(correction.body).toEqual({ typeId: 1001, firstDay: "2026-07-06", lastDay: "2026-07-17" });
    expect(await row("Holiday, Jul 6, 2026 – Jul 17, 2026")).toBeInTheDocument();
  });

  it("says a correction refused in words, and closes on a newer save rather than overwrite it", async () => {
    stubStaffApi({
      role: "manage",
      absence: { people: { 102: graceAsHr() } },
      write: (method, path) =>
        method === "POST" && path.endsWith("/711/correct")
          ? staffProblem(409, {
              code: "absence_status_change",
              title: "Status change",
              detail: "The server's English.",
            })
          : method === "PUT" && path.endsWith("/absences/712")
            ? staffProblem(409, { code: "revision_conflict", title: "Revision conflict", detail: "rev 3" })
            : undefined,
    });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(within(await row(SUMMER)).getByRole("button", { name: "Correct" }));
    let sheet = await screen.findByRole("dialog", { name: "Correct absence" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save correction" }));
    expect(await within(sheet).findByText(/cannot become a registration/)).toBeInTheDocument();
    await userEvent.click(within(sheet).getByRole("button", { name: "Cancel" }));

    await userEvent.click(within(await row(SICK)).getByRole("button", { name: "Change" }));
    sheet = await screen.findByRole("dialog", { name: "Change absence" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Change absence" })).not.toBeInTheDocument());
    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
  });

  it("says a row that is gone is no longer there", async () => {
    stubStaffApi({
      role: "manage",
      absence: { people: { 102: graceAsHr() } },
      write: (_method, path) => (path.endsWith("/711/cancel") ? new Response(null, { status: 404 }) : undefined),
    });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(within(await row(SUMMER)).getByRole("button", { name: "Cancel absence" }));
    await userEvent.click(await screen.findByRole("button", { name: "Cancel the absence" }));
    expect(await screen.findByText("This is no longer there.")).toBeInTheDocument();
  });

  it("changes a registered row and a request with PUT, never /correct", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const sick = await row(SICK);
    expect(within(sick).queryByRole("button", { name: "Correct" })).not.toBeInTheDocument();
    await userEvent.click(within(sick).getByRole("button", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change absence" });
    await typeDate(within(sheet).getByRole("textbox", { name: "Last day" }), "Oct 23, 2026");
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /absences\/712$/)).toHaveLength(1));
    const [change] = sent(fetchMock, "PUT", /absences\/712$/);
    expect(change.ifMatch).toBe('"e712"');
    expect(change.body).toEqual({ typeId: 1003, firstDay: "2026-09-28", lastDay: "2026-10-23", degreePercent: 100 });
  });

  it("registers an absence for the person", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(await screen.findByRole("button", { name: "Register absence" }));
    const sheet = await screen.findByRole("dialog", { name: "Register absence" });
    await choose(sheet, "Type", "Certified sick leave");
    await typeDate(within(sheet).getByRole("textbox", { name: "First day" }), "Nov 2, 2026");
    await userEvent.click(within(sheet).getByRole("button", { name: "Register" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/absences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/absences$/)[0].body).toEqual({
      personId: 102,
      typeId: 1003,
      firstDay: "2026-11-02",
    });
  });

  it("records a demand to postpone holiday for a sykmelding, § 9 explained, and its refusals in words", async () => {
    const fetchMock = stubStaffApi({
      role: "manage",
      absence: { people: { 102: graceAsHr() } },
      write: (_method, path, body) =>
        path.endsWith("/postpone-holiday") && (body as { demandedOn?: string }).demandedOn === "2026-10-01"
          ? staffProblem(409, { code: "holiday_postponement_too_late", title: "Too late", detail: "English" })
          : undefined,
    });
    renderRoute("/staff/people/102?tab=absence");

    expect(within(await row(SUMMER)).queryByRole("button", { name: "Postpone holiday" })).not.toBeInTheDocument();
    await userEvent.click(within(await row(SICK)).getByRole("button", { name: "Postpone holiday" }));
    const dialog = await screen.findByRole("dialog", { name: "Postpone holiday" });
    expect(within(dialog).getByText(/ferieloven § 9/)).toBeInTheDocument();
    const demanded = within(dialog).getByRole("textbox", { name: "Demanded on" });

    await typeDate(demanded, "Oct 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Record the demand" }));
    expect(
      await within(dialog).findByText("The demand came after the last working day before the holiday."),
    ).toBeInTheDocument();

    await typeDate(demanded, "Sep 20, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Record the demand" }));
    expect(await within(dialog).findByText(/never dated before the sykmelding's first day/)).toBeInTheDocument();

    await typeDate(demanded, "Oct 5, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Record the demand" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Postpone holiday" })).not.toBeInTheDocument());
    expect(sent(fetchMock, "POST", /absences\/712\/postpone-holiday$/).map((one) => one.body)).toEqual([
      { demandedOn: "2026-10-01" },
      { demandedOn: "2026-09-20" },
      { demandedOn: "2026-10-05" },
    ]);
  });
});

describe("Person page — the holiday account's adjustments and carry-over", () => {
  it("lists, adds and deletes adjustments", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const adjustments = await region("Holiday adjustments");
    expect(await within(adjustments).findByText(/Agreed carry-over from 2025/)).toBeInTheDocument();

    await userEvent.click(within(adjustments).getByRole("button", { name: "Add adjustment" }));
    const dialog = await screen.findByRole("dialog", { name: "Add adjustment" });
    await retype(within(dialog).getByRole("textbox", { name: "Days" }), "-2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    // A correction says why.
    expect(await within(dialog).findByText("Say why")).toBeInTheDocument();
    await retype(within(dialog).getByRole("textbox", { name: "Note" }), "Counted twice");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /holiday-adjustments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /holiday-adjustments$/)[0].body).toEqual({
      year: 2026,
      days: -2,
      reason: "correction",
      note: "Counted twice",
    });

    await userEvent.click((await within(adjustments).findAllByRole("button", { name: "Delete adjustment" }))[1]);
    await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE", /holiday-adjustments\/801$/)).toHaveLength(1));
  });

  it("carries the year over, and says a second carry-over is already recorded", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(await screen.findByRole("button", { name: "Previous year" }));
    await region("Holiday account 2025");
    await userEvent.click(screen.getByRole("button", { name: "Carry over from 2025" }));
    const dialog = await screen.findByRole("dialog", { name: "Carry over holiday from 2025" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Carry over" }));
    expect(await within(dialog).findByText("A carry-over from that year is already recorded.")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "Next year" }));
    await region("Holiday account 2026");
    await userEvent.click(screen.getByRole("button", { name: "Carry over from 2026" }));
    const next = await screen.findByRole("dialog", { name: "Carry over holiday from 2026" });
    await choose(next, "Kind", /Forced/);
    await userEvent.click(within(next).getByRole("button", { name: "Carry over" }));
    expect(await within(next).findByText("Say why")).toBeInTheDocument();
    await retype(within(next).getByRole("textbox", { name: "Note" }), "Sick through the autumn");
    await userEvent.click(within(next).getByRole("button", { name: "Carry over" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: /Carry over/ })).not.toBeInTheDocument());
    expect(sent(fetchMock, "POST", /people\/102\/carry-over$/).map((one) => one.body)).toEqual([
      { fromYear: 2025, kind: "agreed" },
      { fromYear: 2026, kind: "forced", note: "Sick through the autumn" },
    ]);
  });
});

describe("Person page — the sick-leave case", () => {
  it("shows HR the case with its milestones, the accommodation note, the estimate and Close", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    expect(within(sickCase).getByText("Shorter days for now")).toBeInTheDocument();
    const plan = within(sickCase).getByRole("group", { name: "Follow-up plan" });
    expect(within(plan).getByText("Due")).toBeInTheDocument();
    // No milestone note to HR without staff:personal-data.
    expect(within(sickCase).queryByRole("button", { name: "Change the note" })).not.toBeInTheDocument();

    await userEvent.click(within(plan).getByRole("button", { name: "Mark done" }));
    await waitFor(() =>
      expect(sent(fetchMock, "POST", /cases\/701\/milestones\/follow_up_plan\/done$/)).toHaveLength(1),
    );

    await userEvent.click(within(sickCase).getByRole("button", { name: "Change the estimate" }));
    const estimate = await screen.findByRole("dialog", { name: "52-week end (estimate)" });
    await userEvent.click(within(estimate).getByRole("button", { name: "Reset to first day + 364" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /cases\/701$/)).toHaveLength(1));
    const [reset] = sent(fetchMock, "PUT", /cases\/701$/);
    expect(reset.body).toEqual({ maxBenefitEstimate: null });
    expect(reset.ifMatch).toBe("2");

    await userEvent.click(within(sickCase).getByRole("button", { name: "Close the case" }));
    await userEvent.click(await screen.findByRole("button", { name: "Close it" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /cases\/701\/close$/)).toHaveLength(1));
  });

  it("shows a manager the case without milestone notes and nothing of HR's", async () => {
    stubStaffApi({ role: "access", me: 101, absence: { people: { 102: graceAsManager() } } });
    renderRoute("/staff/people/102?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    expect(within(sickCase).getByText("Shorter days for now")).toBeInTheDocument();
    expect(within(sickCase).getByRole("button", { name: "Change the accommodation note" })).toBeInTheDocument();
    expect(within(sickCase).getAllByRole("button", { name: "Mark done" }).length).toBeGreaterThan(0);
    for (const name of ["Change the estimate", "Close the case", "Change the note"])
      expect(within(sickCase).queryByRole("button", { name })).not.toBeInTheDocument();
    // Nothing of HR's: no adjustments, no carry-over, no withdrawal, no rows to change.
    expect(screen.queryByRole("region", { name: "Holiday adjustments" })).not.toBeInTheDocument();
    for (const name of ["Carry over from 2026", "Withdraw egenmelding", "Register absence", "Correct", "Change"])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    // Care days are not the manager's (reading 34).
    expect(screen.queryByRole("region", { name: /Care days/ })).not.toBeInTheDocument();
  });

  it("shows an Approver the milestones without the accommodation note", async () => {
    stubStaffApi({
      role: "approve",
      absence: { people: { 102: graceAsManager() }, cases: casesAsApprover() },
    });
    renderRoute("/staff/people/102?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    expect(within(sickCase).getByRole("group", { name: "Follow-up plan" })).toBeInTheDocument();
    expect(within(sickCase).queryByText("Accommodation note")).not.toBeInTheDocument();
    expect(within(sickCase).queryByRole("button", { name: "Change the accommodation note" })).not.toBeInTheDocument();
  });

  it("shows staff:personal-data the milestones' notes and writes one with the milestone's revision", async () => {
    const fetchMock = stubStaffApi({
      role: "personalData",
      absence: { people: { 102: graceAsHr() }, cases: casesAsPersonalData() },
    });
    renderRoute("/staff/people/102?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    const plan = within(sickCase).getByRole("group", { name: "Follow-up plan" });
    expect(within(plan).getByText("Plan agreed with Grace")).toBeInTheDocument();
    expect(
      within(within(sickCase).getByRole("group", { name: "NAV's activity requirement" })).getByText(
        "Back at work part-time",
      ),
    ).toBeInTheDocument();

    await userEvent.click(within(plan).getByRole("button", { name: "Change the note" }));
    const dialog = await screen.findByRole("dialog", { name: "Note: Follow-up plan" });
    await retype(within(dialog).getByRole("textbox", { name: "Note" }), "Plan revised");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /milestones\/follow_up_plan\/note$/)).toHaveLength(1));
    const [note] = sent(fetchMock, "PUT", /milestones\/follow_up_plan\/note$/);
    expect(note.body).toEqual({ note: "Plan revised" });
    expect(note.ifMatch).toBe("4");
  });
});

describe("Person page — egenmelding and care days", () => {
  it("withdraws the right to egenmelding and lifts it again", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const standing = await region("Egenmelding");
    await userEvent.click(within(standing).getByRole("button", { name: "Withdraw egenmelding" }));
    const dialog = await screen.findByRole("dialog", { name: "Withdraw egenmelding" });
    await typeDate(within(dialog).getByRole("textbox", { name: "Until" }), "Oct 5, 2026");
    await retype(within(dialog).getByRole("textbox", { name: "Reason" }), "Repeated short absences");
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw" }));
    // Until is after today, at most a year ahead.
    expect(await within(dialog).findByText("A day after today, at most a year ahead")).toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "Until" }), "Jan 31, 2027");
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /self-certification\/withdrawal$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /self-certification\/withdrawal$/)[0].body).toEqual({
      until: "2027-01-31",
      reason: "Repeated short absences",
    });
    expect(await within(standing).findByText(/Repeated short absences/)).toBeInTheDocument();

    await userEvent.click(within(standing).getByRole("button", { name: "Lift the withdrawal" }));
    await userEvent.click(await screen.findByRole("button", { name: "Lift it" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /self-certification\/withdrawal$/)).toHaveLength(2));
    expect(sent(fetchMock, "PUT", /self-certification\/withdrawal$/)[1].body).toEqual({ until: null });
  });

  it("shows a withdrawal only when the standing carries one", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      absence: {
        people: {
          102: {
            ...graceAsManager(),
            selfCertification: selfCertification({ personId: 102 }),
          },
        },
      },
    });
    renderRoute("/staff/people/102?tab=absence");

    const standing = await region("Egenmelding");
    expect(within(standing).queryByText(/withdrawn/i)).not.toBeInTheDocument();
  });

  it("shows HR the care days", async () => {
    stubStaffApi({ role: "manage", absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const care = await region("Care days 2026");
    expect(within(care).getByText("Remaining").nextElementSibling).toHaveTextContent("7");
  });
});

describe("Person page — the Absence tab after review", () => {
  it("offers Postpone holiday only on a registered sykmelding", async () => {
    stubStaffApi({
      role: "manage",
      absence: {
        people: {
          102: {
            ...graceAsHr(),
            absences: graceAbsencesAsHr().map((one) =>
              one.id === 712 ? { ...one, status: "approved" as const } : one,
            ),
          },
        },
      },
    });
    renderRoute("/staff/people/102?tab=absence");

    expect(within(await row(SICK)).queryByRole("button", { name: "Postpone holiday" })).not.toBeInTheDocument();
  });

  it("reads the absences of the year shown, and pages them", async () => {
    const fetchMock = stubStaffApi({ role: "manage", pageSize: 2, absence: { people: { 102: graceAsHr() } } });
    renderRoute("/staff/people/102?tab=absence");

    const list = await region("Absences");
    expect(await within(list).findAllByRole("article")).toHaveLength(2);
    expect(within(list).getByRole("article", { name: CHRISTMAS })).toBeInTheDocument();
    await userEvent.click(within(list).getByRole("button", { name: "2" }));
    expect(await within(list).findByRole("article", { name: SUMMER })).toBeInTheDocument();
    expect(within(list).queryByRole("article", { name: CHRISTMAS })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /absences\?personId=102&from=2026-01-01&to=2026-12-31&page=2/)).toHaveLength(1);

    await userEvent.click(screen.getByRole("button", { name: "Previous year" }));
    await waitFor(() =>
      expect(sent(fetchMock, "GET", /absences\?personId=102&from=2025-01-01&to=2025-12-31&page=1/)).toHaveLength(1),
    );
    expect(await within(list).findByText("No absence this year")).toBeInTheDocument();
  });

  it("offers the accommodation note on the server's word that the caller manages the person, not on a guess", async () => {
    // Ada is an Approver who manages Grace — but not Hedy, whose case she
    // reads as an Approver: no accommodation note to write.
    const hedy = person({
      id: 107,
      givenName: "Hedy",
      familyName: "Lamarr",
      displayName: "Hedy Lamarr",
      employeeNumber: "E-7",
    });
    delete hedy.manager;
    delete hedy.department;
    stubStaffApi({
      role: "approve",
      me: 101,
      people: [...people(), hedy],
      absence: {
        people: { 107: { ...graceAsManager(), absences: [] } },
        cases: casesAsApprover().map((one) => ({ ...one, id: 703, personId: 107 })),
      },
    });
    renderRoute("/staff/people/107?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    expect(within(sickCase).getAllByRole("button", { name: "Mark done" }).length).toBeGreaterThan(0);
    expect(within(sickCase).queryByRole("button", { name: "Change the accommodation note" })).not.toBeInTheDocument();
  });

  it("offers the person on their own page nothing of the case to change", async () => {
    stubStaffApi({ role: "access", me: 102, absence: { people: { 102: graceAsManager() } } });
    renderRoute("/staff/people/102?tab=absence");

    const sickCase = await screen.findByRole("article", { name: "Sick-leave case from Sep 28, 2026" });
    expect(within(sickCase).getByText("Shorter days for now")).toBeInTheDocument();
    for (const name of ["Mark done", "Waive", "Change the accommodation note"])
      expect(within(sickCase).queryByRole("button", { name })).not.toBeInTheDocument();
  });

  it("puts a refused withdrawal's field error on its field", async () => {
    stubStaffApi({
      role: "manage",
      absence: { people: { 102: graceAsHr() } },
      write: (method, path) =>
        method === "PUT" && path.endsWith("/self-certification/withdrawal")
          ? staffProblem(400, {
              title: "Invalid withdrawal",
              errors: { until: ["The withdrawal ends after today, at most a year ahead"] },
            })
          : undefined,
    });
    renderRoute("/staff/people/102?tab=absence");

    await userEvent.click(within(await region("Egenmelding")).getByRole("button", { name: "Withdraw egenmelding" }));
    const dialog = await screen.findByRole("dialog", { name: "Withdraw egenmelding" });
    await typeDate(within(dialog).getByRole("textbox", { name: "Until" }), "Jan 31, 2027");
    await retype(within(dialog).getByRole("textbox", { name: "Reason" }), "Repeated short absences");
    await userEvent.click(within(dialog).getByRole("button", { name: "Withdraw" }));
    expect(
      await within(dialog).findByText("The withdrawal ends after today, at most a year ahead"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Until" })).toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).getByRole("textbox", { name: "Reason" })).not.toHaveAttribute("aria-invalid", "true");
  });
});

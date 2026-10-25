import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
  availabilityDeclarations,
  competenceReviews,
  personCompetences,
  personInterests,
  personPreferences,
} from "../test/competence-fixtures";
import { peopleWithRecords } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

/** One row of a section's list, by what it names — not the editor's pills of the same names. */
const item = async (section: HTMLElement, text: string) => {
  const list = await within(section).findByRole("list");
  return (await within(list).findByText(text)).closest("li") as HTMLElement;
};

describe("My page — What I want", () => {
  it("shows my interests, preferences and availability, mine to edit and none to stamp", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const wishes = await region("What I want");
    expect(within(wishes).queryByRole("button", { name: "Mark as seen" })).not.toBeInTheDocument();

    const interests = await within(wishes).findByRole("region", { name: "Interests" });
    const welding = await item(interests, "Welding");
    expect(within(welding).getByText("Wants to use more")).toBeInTheDocument();
    expect(within(welding).getByText("Seen Sep 15, 2026")).toBeInTheDocument();
    const english = await item(interests, "English");
    expect(within(english).getByText("Wants to learn")).toBeInTheDocument();
    // Mine: nothing is "new" to me.
    expect(within(interests).queryByText("New")).not.toBeInTheDocument();
    expect(within(interests).getByRole("combobox", { name: "Your interests" })).toBeInTheDocument();

    const preferences = await within(wishes).findByRole("region", { name: "Preferences" });
    const weekdays = within(preferences).getByRole("group", { name: "Weekdays" });
    expect(within(weekdays).getByRole("switch", { name: "I have a preference" })).toBeChecked();
    for (const day of ["Monday", "Tuesday", "Wednesday"])
      expect(within(weekdays).getByRole("checkbox", { name: day })).toBeChecked();
    expect(within(weekdays).getByRole("checkbox", { name: "Friday" })).not.toBeChecked();
    expect(within(weekdays).getByRole("radio", { name: "Matters" })).toBeChecked();
    const timeOfDay = within(preferences).getByRole("group", { name: "Time of day" });
    expect(within(timeOfDay).getByRole("radio", { name: "Important" })).toBeChecked();
    expect(
      within(within(preferences).getByRole("group", { name: "Travel" })).getByRole("switch", {
        name: "I have a preference",
      }),
    ).not.toBeChecked();

    const availability = await within(wishes).findByRole("region", { name: "Availability" });
    const [, row] = within(availability).getAllByRole("row");
    expect(row).toHaveTextContent("Unavailable");
    expect(row).toHaveTextContent("Every Friday");
    expect(row).toHaveTextContent("12:00–24:00");
  });

  it("saves my interests whole", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const interests = await within(await region("What I want")).findByRole("region", { name: "Interests" });
    // Neither a hidden nor an archived competence is offered.
    await userEvent.click(within(interests).getByRole("combobox", { name: "Your interests" }));
    expect(screen.queryByRole("option", { name: "First aid" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Lotus 1-2-3" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("option", { name: "Excel" }));
    await userEvent.click(within(interests).getByRole("button", { name: "Save interests" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/interests$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/people\/101\/interests$/);
    expect(put.body).toEqual({ interests: [{ competenceId: 2004 }, { competenceId: 2005 }, { competenceId: 2006 }] });
    expect(put.ifMatch).toBeNull();
    const excel = await item(interests, "Excel");
    expect(within(excel).getByText("Wants to learn")).toBeInTheDocument();
    // The interest kept keeps its stamp.
    expect(within(await item(interests, "Welding")).getByText("Seen Sep 15, 2026")).toBeInTheDocument();
  });

  it("puts a refusal at an index on the banner, naming the row", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path.endsWith("/interests")
          ? staffProblem(400, {
              code: "competence_unavailable",
              title: "Invalid interests",
              errors: { "interests[1].competenceId": ["Choose a competence that is offered"] },
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=competence");

    const interests = await within(await region("What I want")).findByRole("region", { name: "Interests" });
    await choose(interests, "Your interests", "Excel");
    await userEvent.click(within(interests).getByRole("button", { name: "Save interests" }));

    expect(await within(interests).findByText("English: This competence is no longer offered.")).toBeInTheDocument();
  });

  it("saves my preferences whole, one per kind with its weight", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const preferences = await within(await region("What I want")).findByRole("region", { name: "Preferences" });
    const remote = within(preferences).getByRole("group", { name: "Remote work" });
    await userEvent.click(within(remote).getByRole("switch", { name: "I have a preference" }));
    // Turned on but not filled in, it is not sent.
    await userEvent.click(within(preferences).getByRole("button", { name: "Save preferences" }));
    expect(await within(remote).findByText("Complete the preference, or turn it off")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/preferences$/)).toHaveLength(0);

    await choose(remote, "How much", "Some days");
    await userEvent.click(within(remote).getByRole("radio", { name: "Important" }));
    await userEvent.click(within(preferences).getByRole("button", { name: "Save preferences" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/preferences$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/people\/101\/preferences$/)[0].body).toEqual({
      preferences: [
        { kind: "weekdays", value: { days: [1, 2, 3] }, weight: 2 },
        { kind: "time_of_day", value: { parts: ["day"] }, weight: 3 },
        { kind: "remote", value: { share: "some" }, weight: 3 },
      ],
    });
  });

  it("picks a location preference from the company's places, without the directory", async () => {
    // staff:access alone: no structure, but /meta's locations all the same.
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const preferences = await within(await region("What I want")).findByRole("region", { name: "Preferences" });
    const location = within(preferences).getByRole("group", { name: "Location" });
    await userEvent.click(within(location).getByRole("switch", { name: "I have a preference" }));
    await choose(location, "Locations", "Bergen");
    await userEvent.click(within(preferences).getByRole("button", { name: "Save preferences" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/preferences$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/people\/101\/preferences$/)[0].body).toEqual({
      preferences: [
        { kind: "weekdays", value: { days: [1, 2, 3] }, weight: 2 },
        { kind: "time_of_day", value: { parts: ["day"] }, weight: 3 },
        { kind: "location", value: { locationIds: [32] }, weight: 2 },
      ],
    });
  });

  it("adds a dated declaration and saves the availability whole", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const availability = await within(await region("What I want")).findByRole("region", { name: "Availability" });
    await userEvent.click(within(availability).getByRole("button", { name: "Add declaration" }));
    const dialog = await screen.findByRole("dialog", { name: "Add declaration" });
    await choose(dialog, "Kind", "Preferred");
    await userEvent.click(within(dialog).getByRole("radio", { name: "On dates" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await within(dialog).findByText("Give the first day")).toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: /^First day/ }), "Nov 3, 2026");
    await typeDate(within(dialog).getByRole("textbox", { name: "Last day" }), "Nov 4, 2026");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Part of the day" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add declaration" })).not.toBeInTheDocument());

    const rows = within(availability).getAllByRole("row");
    expect(rows[2]).toHaveTextContent("Preferred");
    expect(rows[2]).toHaveTextContent("Nov 3, 2026 – Nov 4, 2026");
    expect(rows[2]).toHaveTextContent("08:00–16:00");
    await userEvent.click(within(availability).getByRole("button", { name: "Save availability" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/101\/availability$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/people\/101\/availability$/)[0].body).toEqual({
      declarations: [
        { kind: "unavailable", weekday: 5, startMinute: 720, endMinute: 1440 },
        { kind: "preferred", fromDate: "2026-11-03", toDate: "2026-11-04", startMinute: 480, endMinute: 960 },
      ],
    });
  });

  it("removes a declaration before saving", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const availability = await within(await region("What I want")).findByRole("region", { name: "Availability" });
    await userEvent.click(within(availability).getByRole("button", { name: "Remove Every Friday" }));
    expect(within(availability).getByText("Nothing declared")).toBeInTheDocument();
    await userEvent.click(within(availability).getByRole("button", { name: "Save availability" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/availability$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/availability$/)[0].body).toEqual({ declarations: [] });
  });

  it("puts a preference's refusal at its index on the banner, naming the kind", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path.endsWith("/preferences")
          ? staffProblem(400, {
              title: "Invalid preferences",
              errors: { "preferences[1].value": ["The parts are 1 to 4 values"] },
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=competence");

    const preferences = await within(await region("What I want")).findByRole("region", { name: "Preferences" });
    await userEvent.click(within(preferences).getByRole("button", { name: "Save preferences" }));

    expect(await within(preferences).findByText("Time of day: The parts are 1 to 4 values")).toBeInTheDocument();
  });

  it("puts a declaration's refusal on the row alone on the banner, naming the row", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path.endsWith("/availability")
          ? staffProblem(400, {
              title: "Invalid availability",
              errors: { "declarations[0]": ["This row is given twice"] },
            })
          : undefined,
    });
    renderRoute("/staff/me?tab=competence");

    const availability = await within(await region("What I want")).findByRole("region", { name: "Availability" });
    await userEvent.click(within(availability).getByRole("button", { name: "Add declaration" }));
    const dialog = await screen.findByRole("dialog", { name: "Add declaration" });
    await choose(dialog, /^Weekday/, "Monday");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add declaration" })).not.toBeInTheDocument());
    await userEvent.click(within(availability).getByRole("button", { name: "Save availability" }));

    expect(await within(availability).findByText("Every Friday: This row is given twice")).toBeInTheDocument();
  });

  it("says in words that an erased record takes no write", async () => {
    stubStaffApi({
      role: "access",
      me: 101,
      write: (method, path) =>
        method === "PUT" && path.endsWith("/interests")
          ? staffProblem(409, { code: "person_erased", title: "Person erased", detail: "The server's English." })
          : undefined,
    });
    renderRoute("/staff/me?tab=competence");

    const interests = await within(await region("What I want")).findByRole("region", { name: "Interests" });
    await choose(interests, "Your interests", "Excel");
    await userEvent.click(within(interests).getByRole("button", { name: "Save interests" }));

    expect(
      await within(interests).findByText("This record is erased and can no longer be changed."),
    ).toBeInTheDocument();
    expect(screen.queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("keeps an unsaved draft when the rows change elsewhere, until I reload it", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    const { queryClient } = renderRoute("/staff/me?tab=competence");

    const wishes = await region("What I want");
    const interests = await within(wishes).findByRole("region", { name: "Interests" });
    const availability = await within(wishes).findByRole("region", { name: "Availability" });
    await choose(interests, "Your interests", "Excel");
    const save = within(interests).getByRole("button", { name: "Save interests" });
    // What the draft holds: the options it has chosen.
    const chosen = (name: string) => screen.getByRole("option", { name, hidden: true });
    expect(save).toBeEnabled();

    // Elsewhere, English is dropped and a dated declaration added.
    fetchMock.state.interests = fetchMock.state.interests.filter(
      (one) => !(one.personId === 101 && one.competenceId === 2005),
    );
    fetchMock.state.availability.push({ personId: 101, kind: "preferred", fromDate: "2026-12-01" });
    await act(() => queryClient.invalidateQueries());

    // My draft stands — English chosen, no longer on the list — and says the rows changed under it.
    await waitFor(() =>
      expect(within(within(interests).getByRole("list", { name: "Interests" })).queryByText("English")).toBeNull(),
    );
    expect(chosen("English")).toHaveAttribute("aria-selected", "true");
    expect(chosen("Excel")).toHaveAttribute("aria-selected", "true");
    expect(within(interests).getByText("Changed elsewhere — reload to see it")).toBeInTheDocument();
    expect(save).toBeEnabled();
    // The pristine editor follows the server.
    expect(await within(availability).findByText("Dec 1, 2026")).toBeInTheDocument();
    expect(within(availability).queryByText("Changed elsewhere — reload to see it")).not.toBeInTheDocument();
    expect(within(availability).getByRole("button", { name: "Save availability" })).toBeDisabled();

    await userEvent.click(within(interests).getByRole("button", { name: "Reload" }));
    expect(within(interests).queryByText("Changed elsewhere — reload to see it")).not.toBeInTheDocument();
    expect(save).toBeDisabled();
    expect(chosen("English")).toHaveAttribute("aria-selected", "false");
    expect(chosen("Excel")).toHaveAttribute("aria-selected", "false");
  });

  it("lights up Save only for a real change, not for a row removed and added back", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const availability = await within(await region("What I want")).findByRole("region", { name: "Availability" });
    const save = within(availability).getByRole("button", { name: "Save availability" });
    await userEvent.click(within(availability).getByRole("button", { name: "Remove Every Friday" }));
    expect(save).toBeEnabled();
    await userEvent.click(within(availability).getByRole("button", { name: "Add declaration" }));
    const dialog = await screen.findByRole("dialog", { name: "Add declaration" });
    await choose(dialog, /^Weekday/, "Friday");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Part of the day" }));
    await choose(dialog, "Starts", "12:00");
    await choose(dialog, "Ends", "24:00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add declaration" })).not.toBeInTheDocument());

    expect(within(availability).getByText("Every Friday")).toBeInTheDocument();
    expect(save).toBeDisabled();
  });

  it("offers a start up to 23:45 and an end from 00:15 to 24:00, and forgets a duplicate once changed", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const availability = await within(await region("What I want")).findByRole("region", { name: "Availability" });
    await userEvent.click(within(availability).getByRole("button", { name: "Add declaration" }));
    const dialog = await screen.findByRole("dialog", { name: "Add declaration" });
    await choose(dialog, /^Weekday/, "Friday");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Part of the day" }));

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Starts" }));
    expect(screen.getByRole("option", { name: "23:45" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "24:00" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "12:00" }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Ends" }));
    expect(screen.queryByRole("option", { name: "00:00" })).not.toBeInTheDocument();
    expect(screen.getByRole("option", { name: "00:15" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: "24:00" }));

    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await within(dialog).findByText("This declaration is already in the list")).toBeInTheDocument();
    await choose(dialog, "Kind", "Preferred");
    expect(within(dialog).queryByText("This declaration is already in the list")).not.toBeInTheDocument();
  });
});

describe("The person page — What they want", () => {
  it("is read-only to the manager, who marks it seen", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=competence");

    const wishes = await region("What they want");
    const interests = await within(wishes).findByRole("region", { name: "Interests" });
    const welding = await item(interests, "Welding");
    expect(within(welding).getByText("Wants to learn")).toBeInTheDocument();
    expect(within(welding).getByText("New")).toBeInTheDocument();
    expect(within(wishes).queryByRole("combobox")).not.toBeInTheDocument();
    for (const action of ["Save interests", "Save preferences", "Save availability", "Add declaration"])
      expect(within(wishes).queryByRole("button", { name: action })).not.toBeInTheDocument();
    const remote = await item(await within(wishes).findByRole("region", { name: "Preferences" }), "Remote work");
    expect(remote).toHaveTextContent("Remote work: Mostly");
    expect(within(remote).getByText("Matters")).toBeInTheDocument();
    const [, away] = within(await within(wishes).findByRole("region", { name: "Availability" })).getAllByRole("row");
    expect(away).toHaveTextContent("Nov 2, 2026 – Nov 6, 2026");
    expect(away).toHaveTextContent("All day");

    await userEvent.click(within(wishes).getByRole("button", { name: "Mark as seen" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/102\/wishes\/seen$/)).toHaveLength(1));
    expect(await within(welding).findByText("Seen Oct 11, 2026")).toBeInTheDocument();
    expect(within(wishes).queryByText("New")).not.toBeInTheDocument();
    // Grace's declared competence is acknowledged with the wishes.
    const english = await screen.findByRole("article", { name: "English" });
    expect(await within(english).findByText("Oct 11, 2026")).toBeInTheDocument();
    await waitFor(() => expect(within(wishes).getByRole("button", { name: "Mark as seen" })).toBeDisabled());
  });

  it("offers HR the Seen too", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102?tab=competence");

    const wishes = await region("What they want");
    expect(within(wishes).getByRole("button", { name: "Mark as seen" })).toBeInTheDocument();
  });

  it("is read-only to staff:personal-data alone, with nothing to stamp and nothing new", async () => {
    stubStaffApi({ role: "personalDataOnly" });
    renderRoute("/staff/people/102?tab=competence");

    const wishes = await region("What they want");
    const welding = await item(await within(wishes).findByRole("region", { name: "Interests" }), "Welding");
    expect(within(welding).getByText("Wants to learn")).toBeInTheDocument();
    expect(within(wishes).queryByRole("button", { name: "Mark as seen" })).not.toBeInTheDocument();
    expect(within(wishes).queryByText("New")).not.toBeInTheDocument();
  });

  it("still reads an erased person's wishes, with neither Seen nor Reviewed today", async () => {
    stubStaffApi({
      role: "manage",
      people: peopleWithRecords().map((one) => (one.id === 102 ? { ...one, erasedAt: "2026-10-01T02:00:00Z" } : one)),
      // Reviewed once, so the review block is there to show no Reviewed today.
      reviews: [
        ...competenceReviews(),
        {
          id: 4002,
          personId: 102,
          reviewedOn: "2026-05-04",
          reviewer: "Ada Lovelace",
          recordedAt: "2026-05-04T10:00:00Z",
        },
      ],
    });
    renderRoute("/staff/people/102?tab=competence");

    const wishes = await region("What they want");
    expect(await item(await within(wishes).findByRole("region", { name: "Interests" }), "Welding")).toBeInTheDocument();
    expect(within(wishes).queryByRole("button", { name: "Mark as seen" })).not.toBeInTheDocument();
    const reviews = await region("Reviewed together");
    expect(within(reviews).getByText("May 4, 2026 by Ada Lovelace")).toBeInTheDocument();
    expect(within(reviews).queryByRole("button", { name: "Reviewed today" })).not.toBeInTheDocument();
  });

  it("has Seen disabled from the start when every row carries its stamp", async () => {
    const stamped = "2026-10-01T09:00:00Z";
    stubStaffApi({
      role: "access",
      me: 101,
      interests: personInterests().map((one) => ({ ...one, seenAt: stamped })),
      preferences: personPreferences().map((one) => ({ ...one, seenAt: stamped })),
      availability: availabilityDeclarations().map((one) => ({ ...one, seenAt: stamped })),
      personCompetences: personCompetences().map((one) => ({ ...one, seenAt: stamped })),
    });
    renderRoute("/staff/people/102?tab=competence");

    const wishes = await region("What they want");
    await item(await within(wishes).findByRole("region", { name: "Interests" }), "Welding");
    await screen.findByRole("article", { name: "English" });
    expect(within(wishes).getByRole("button", { name: "Mark as seen" })).toBeDisabled();
    expect(within(wishes).queryByText("New")).not.toBeInTheDocument();
  });

  it("is absent to a colleague, who reads none of it", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    renderRoute("/staff/people/102?tab=competence");

    await screen.findByRole("article", { name: "English" });
    expect(screen.queryByRole("region", { name: "What they want" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Reviewed together" })).not.toBeInTheDocument();
    for (const list of [/\/interests/, /\/preferences/, /\/availability/])
      expect(sent(fetchMock, "GET", list)).toHaveLength(0);
  });

  it("is absent to an Approver, who records reviews all the same", async () => {
    const fetchMock = stubStaffApi({ role: "approve" });
    renderRoute("/staff/people/102?tab=competence");

    expect(await region("Reviewed together")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "What they want" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/interests/)).toHaveLength(0);
  });
});

describe("Reviewed together", () => {
  it("records today's review with the person, then shows it as the last", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=competence");

    const reviews = await region("Reviewed together");
    expect(within(reviews).getByText("Never")).toBeInTheDocument();
    await userEvent.click(within(reviews).getByRole("button", { name: "Reviewed today" }));
    const confirm = await screen.findByRole("dialog", { name: "Reviewed together today?" });
    expect(within(confirm).getByText(/you and Grace Hopper/)).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Reviewed today" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/102\/competence-reviews$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/competence-reviews$/)[0].body).toEqual({});
    expect(await within(reviews).findByText("Oct 11, 2026 by Ada Lovelace")).toBeInTheDocument();
  });

  it("shows the person their last review and the earlier ones, with nothing to record", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const reviews = await region("Reviewed together");
    expect(within(reviews).getByText("Nov 1, 2025 by Hedy Lamarr")).toBeInTheDocument();
    expect(within(reviews).queryByRole("button", { name: "Reviewed today" })).not.toBeInTheDocument();
    await userEvent.click(within(reviews).getByRole("switch", { name: "Show earlier reviews" }));
    const earlier = await within(reviews).findByRole("list", { name: "Earlier reviews" });
    expect(within(earlier).getByText("Nov 1, 2025 — Hedy Lamarr")).toBeInTheDocument();
  });
});

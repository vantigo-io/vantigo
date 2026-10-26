import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { validatePersonSearch } from "../lib/search";
import { people } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const heading = (name: string) => screen.findByRole("heading", { name, level: 2 });

describe("Person page — the profile as HR reads it", () => {
  it("shows every group the response carries, the sealed fields as on file", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await heading("Ada Lovelace");
    const work = await region("Work");
    expect(within(work).getByText("ada@fjord.example")).toBeInTheDocument();
    expect(within(work).getByText("E-1")).toBeInTheDocument();
    expect(within(work).getByText("Senior engineer")).toBeInTheDocument();
    expect(within(work).getByText("Oslo office")).toBeInTheDocument();
    expect(within(work).getByText("Oslo HQ")).toBeInTheDocument();
    const personal = await region("Private");
    expect(within(personal).getByText("+47 912 34 567")).toBeInTheDocument();
    expect(within(personal).getByText("ada@home.example")).toBeInTheDocument();
    expect(within(await region("HR's notes")).getByText("Asked for a standing desk")).toBeInTheDocument();

    const identifiers = await region("Identifiers");
    expect(within(identifiers).getByText("On file")).toBeInTheDocument();
    expect(within(identifiers).getByText("Ending in 8903")).toBeInTheDocument();
    // No digit of the national id is ever drawn outside a reveal (design S4).
    expect(document.body.textContent).not.toMatch(/10129049905|101290/);
    // staff:manage alone neither reveals nor sets them.
    expect(within(identifiers).queryByRole("button", { name: /Reveal/ })).not.toBeInTheDocument();
    expect(within(identifiers).queryByRole("button", { name: /Set/ })).not.toBeInTheDocument();

    expect(within(await region("Next of kin")).getByText("Byron Lovelace")).toBeInTheDocument();
    expect(within(await region("Roles")).getByText("Safety representative (verneombud)")).toBeInTheDocument();
    expect(within(await region("Self-service")).getByText(/Ada at Fjord/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit details" })).toBeInTheDocument();
  });

  it("shows the direct reports", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    const org = await region("Organisation");
    expect(await within(org).findByRole("link", { name: "Grace Hopper" })).toBeInTheDocument();
  });

  it("shows the manager chain up", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102");

    const org = await region("Organisation");
    expect(await within(org).findByRole("link", { name: "Ada Lovelace" })).toBeInTheDocument();
  });
});

describe("Person page — the profile as others read it", () => {
  it("shows a colleague the work group and the roles, nothing private and nothing to change", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people/101");

    const work = await region("Work");
    expect(within(work).getByText("ada@fjord.example")).toBeInTheDocument();
    // The title, the unit and the location today are the work group's, everyone's.
    expect(within(work).getByText("Senior engineer")).toBeInTheDocument();
    expect(within(work).getByText("Oslo office")).toBeInTheDocument();
    expect(within(work).getByText("Oslo HQ")).toBeInTheDocument();
    expect(await region("Roles")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Private" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "HR's notes" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Identifiers" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Next of kin" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Self-service" })).not.toBeInTheDocument();
    expect(screen.queryByText("+47 912 34 567")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit details" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit roles" })).not.toBeInTheDocument();
  });

  it("shows a manager their report's work group, not the private one", async () => {
    // The caller is Ada, linked; Grace's terms name Ada as her manager.
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102");

    await heading("Grace Hopper");
    expect(within(await region("Work")).getByText("grace@fjord.example")).toBeInTheDocument();
    expect(screen.queryByText("+47 999 88 777")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Private" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Identifiers" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit details" })).not.toBeInTheDocument();
  });

  it.each([
    ["a colleague", "view"],
    ["HR", "manage"],
  ] as const)("shows %s the chain of whoever manages today, a manager who has left skipped", async (_, role) => {
    // Grace's manager is Ada, whose terms name Edsger, who has left: Ada
    // heads the top department, so nobody manages her today, and the chain
    // above Grace is Ada alone — for a colleague and HR alike.
    stubStaffApi({
      role,
      people: people().map((one) =>
        one.id === 101 ? { ...one, manager: { id: 104, displayName: "Edsger Dijkstra", employeeNumber: "E-4" } } : one,
      ),
    });
    renderRoute("/staff/people/102");

    const org = await region("Organisation");
    expect(await within(org).findByRole("link", { name: "Ada Lovelace" })).toBeInTheDocument();
    expect(within(org).queryByText("Edsger Dijkstra")).not.toBeInTheDocument();
  });

  it("shows staff:personal-data alone every group and Reveal, and nothing to edit", async () => {
    const fetchMock = stubStaffApi({ role: "personalDataOnly" });
    renderRoute("/staff/people/101");

    await heading("Ada Lovelace");
    expect(within(await region("Private")).getByText("+47 912 34 567")).toBeInTheDocument();
    expect(within(await region("HR's notes")).getByText("Asked for a standing desk")).toBeInTheDocument();
    expect(within(await region("Next of kin")).getByText("Byron Lovelace")).toBeInTheDocument();
    expect(await region("Roles")).toBeInTheDocument();
    for (const name of ["Edit details", "Edit next of kin", "Edit roles", "Link", "Unlink"])
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();

    const identifiers = await region("Identifiers");
    await userEvent.click(within(identifiers).getByRole("button", { name: "Reveal the national identity number" }));
    const confirm = await screen.findByRole("dialog", { name: "Reveal the national identity number?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Reveal" }));
    expect(await within(identifiers).findByText("10129049905")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /national-id\/reveal$/)).toHaveLength(1);
  });

  it("says a person outside the caller's reach is not there, as an unknown id", async () => {
    stubStaffApi({ role: "access" });
    renderRoute("/staff/people/101");

    expect(await screen.findByText("This person is not in Staff, or is not yours to see")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Ada Lovelace" })).not.toBeInTheDocument();
  });
});

describe("Person page — HR changes the record", () => {
  it("saves the details with the revision it read, the employee number left alone", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    expect(within(dialog).queryByRole("textbox", { name: "Employee number" })).not.toBeInTheDocument();
    await retype(within(dialog).getByRole("textbox", { name: "Work phone" }), "+47 22 00 00 00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/people\/101$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/people\/101$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toMatchObject({
      givenName: "Ada",
      familyName: "Lovelace",
      phoneWork: "+47 22 00 00 00",
      phoneMobile: "+47 912 34 567",
      notes: "Asked for a standing desk",
    });
    expect(put.body).not.toHaveProperty("employeeNumber");
    expect(await within(await region("Work")).findByText("+47 22 00 00 00")).toBeInTheDocument();
  });

  it("keeps the care-day fields it does not show on the replace it sends", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    const ada = fetchMock.state.people.find((one) => one.id === 101);
    if (ada?.record) Object.assign(ada.record, { childrenUnder12: 2, soleCarer: true, chronicallyIllChildren: 1 });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    await retype(within(dialog).getByRole("textbox", { name: "Work phone" }), "+47 22 00 00 00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/people\/101$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/people\/101$/);
    // PUT /people/{id} is a full replace that clears what it leaves out, so a
    // modal that does not show the care-day fields still sends them back.
    expect(put.body).toMatchObject({ childrenUnder12: 2, soleCarer: true, chronicallyIllChildren: 1 });
  });

  it("sends no care-day field the person does not have", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/people\/101$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/people\/101$/);
    for (const field of ["childrenUnder12", "soleCarer", "chronicallyIllChildren"])
      expect(put.body).not.toHaveProperty(field);
  });

  it("reloads with the reader's sentence when somebody else saved first", async () => {
    const fetchMock = stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/people/101"
          ? staffProblem(409, {
              code: "revision_conflict",
              title: "Revision conflict",
              detail: "The person has revision 4; the supplied revision was 3.",
            })
          : undefined,
    });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    const ada = fetchMock.state.people.find((one) => one.id === 101);
    if (ada) ada.phoneWork = "+47 23 23 23 23";
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit details" })).not.toBeInTheDocument());
    expect(await within(await region("Work")).findByText("+47 23 23 23 23")).toBeInTheDocument();
  });

  it("replaces the next of kin and then the roles, each with the person's revision as it moved", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit next of kin" }));
    let dialog = await screen.findByRole("dialog", { name: "Edit next of kin" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Add next of kin" }));
    await userEvent.type(within(dialog).getAllByRole("textbox", { name: "Name" })[1], "Annabella Byron");
    await choose(dialog, "Relationship", "Parent", 1);
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/next-of-kin$/)).toHaveLength(1));
    const [kin] = sent(fetchMock, "PUT", /\/next-of-kin$/);
    expect(kin.ifMatch).toBe("3");
    expect(kin.body).toEqual({
      nextOfKin: [
        { name: "Byron Lovelace", relationship: "spouse", phone: "+47 400 00 000" },
        { name: "Annabella Byron", relationship: "parent" },
      ],
    });
    expect(await within(await region("Next of kin")).findByText("Annabella Byron")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Edit roles" }));
    dialog = await screen.findByRole("dialog", { name: "Edit roles" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Add role" }));
    await choose(dialog, "Role", "HSE responsible", 1);
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/roles$/)).toHaveLength(1));
    const [roles] = sent(fetchMock, "PUT", /\/roles$/);
    // The next of kin moved the person's revision on; the roles carry the new one.
    expect(roles.ifMatch).toBe("4");
    expect(roles.body).toEqual({
      roles: [{ role: "verneombud", fromDate: "2026-01-01" }, { role: "hms_responsible" }],
    });
  });
});

describe("Person page — the sealed fields", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("reveals the national id after a confirmation, for thirty seconds, never cached", async () => {
    // Fake clocks that still move with real time, so the user events and
    // the queries run while the test steps the countdown by hand.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const fetchMock = stubStaffApi({ role: "personalData" });
    const { queryClient } = renderRoute("/staff/people/101");

    const identifiers = await region("Identifiers");
    await userEvent.click(within(identifiers).getByRole("button", { name: "Reveal the national identity number" }));
    const confirm = await screen.findByRole("dialog", { name: "Reveal the national identity number?" });
    expect(within(confirm).getByText(/recorded in the person's history/)).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /reveal$/)).toHaveLength(0);
    await userEvent.click(within(confirm).getByRole("button", { name: "Reveal" }));

    expect(await within(identifiers).findByText("10129049905")).toBeInTheDocument();
    expect(within(identifiers).getByText("Hidden in 30 s")).toBeInTheDocument();
    const [reveal] = sent(fetchMock, "POST", /national-id\/reveal$/);
    expect(reveal.cache).toBe("no-store");
    // Not in the query cache, nor kept by the mutation once it is shown.
    const cached = JSON.stringify([
      queryClient
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data),
      queryClient
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state.data),
    ]);
    expect(cached).not.toContain("10129049905");

    act(() => {
      vi.advanceTimersByTime(10_000);
    });
    expect(within(identifiers).getByText("Hidden in 20 s")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(20_000);
    });
    await waitFor(() => expect(within(identifiers).queryByText("10129049905")).not.toBeInTheDocument());
    expect(within(identifiers).getByText("On file")).toBeInTheDocument();
  });

  it("hides the bank account at once on Hide", async () => {
    stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101");

    const identifiers = await region("Identifiers");
    await userEvent.click(within(identifiers).getByRole("button", { name: "Reveal the bank account" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Reveal the bank account?" })).getByRole("button", {
        name: "Reveal",
      }),
    );
    expect(await within(identifiers).findByText("12345678903")).toBeInTheDocument();
    await userEvent.click(within(identifiers).getByRole("button", { name: "Hide" }));
    expect(within(identifiers).queryByText("12345678903")).not.toBeInTheDocument();
  });

  it("sets a national id, saying so on the number when it disagrees with the birth date", async () => {
    const fetchMock = stubStaffApi({ role: "personalData" });
    const { queryClient } = renderRoute("/staff/people/101");
    // What a Set mutation holds once settled: never the number typed (S4).
    const kept = () =>
      JSON.stringify(
        queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state.variables) ?? [],
      );

    const identifiers = await region("Identifiers");
    await userEvent.click(within(identifiers).getByRole("button", { name: "Set the national identity number" }));
    const dialog = await screen.findByRole("dialog", { name: "Set the national identity number" });
    await choose(dialog, "Kind", "Fødselsnummer");
    await userEvent.type(within(dialog).getByLabelText(/^Number/), "01019012345");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(
      await within(dialog).findByText(
        "The number encodes another birth date than the person's; correct whichever is wrong",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/^Number/)).toHaveAttribute("aria-invalid", "true");
    expect(kept()).not.toContain("01019012345");

    await retype(within(dialog).getByLabelText(/^Number/), "101290 49905");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Set the national identity number" })).not.toBeInTheDocument(),
    );
    expect(sent(fetchMock, "PUT", /national-id$/).at(-1)?.body).toEqual({
      kind: "fodselsnummer",
      value: "101290 49905",
    });
    expect(kept()).not.toContain("49905");
    // Nothing of what was typed stays on the page.
    expect(document.body.textContent).not.toMatch(/49905/);
  });

  it("sets a bank account and shows its last four digits", async () => {
    const fetchMock = stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101");

    const identifiers = await region("Identifiers");
    await userEvent.click(within(identifiers).getByRole("button", { name: "Set the bank account" }));
    const dialog = await screen.findByRole("dialog", { name: "Set the bank account" });
    await choose(dialog, "Kind", "Norwegian account number");
    await userEvent.type(within(dialog).getByLabelText(/^Account number/), "1503.12.34567");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(identifiers).findByText("Ending in 4567")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /bank-account$/)[0].body).toEqual({ kind: "bban", value: "1503.12.34567" });
  });
});

describe("Person page — the self-service link", () => {
  it("links a person to a user found by name, as linked and not managed", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/105");

    const link = await region("Self-service");
    expect(within(link).getByText("Not linked to a user")).toBeInTheDocument();
    await userEvent.type(within(link).getByRole("combobox", { name: "User" }), "Grace");
    await userEvent.click(await screen.findByRole("option", { name: "Grace at Fjord" }, { timeout: 10_000 }));
    await userEvent.click(within(link).getByRole("button", { name: "Link" }));

    expect(await within(link).findByText(/Grace at Fjord — linked, not managed by Staff/)).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/people\/105\/user$/)[0].body).toEqual({
      userId: "8d5c6a3e-0000-4000-8000-000000000102",
    });
    expect(sent(fetchMock, "GET", /assignable-users\?q=Grace/).length).toBeGreaterThan(0);
  });

  it("puts the server's refusal on the picker when the user is linked elsewhere", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/105");

    const link = await region("Self-service");
    await userEvent.type(within(link).getByRole("combobox", { name: "User" }), "Ada");
    await userEvent.click(await screen.findByRole("option", { name: "Ada at Fjord" }, { timeout: 10_000 }));
    await userEvent.click(within(link).getByRole("button", { name: "Link" }));

    expect(
      await within(link).findByText("This user is linked to another person; unlink them there first"),
    ).toBeInTheDocument();
  });

  it("unlinks after asking", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    const link = await region("Self-service");
    await userEvent.click(within(link).getByRole("button", { name: "Unlink" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Unlink Ada at Fjord?" })).getByRole("button", {
        name: "Unlink",
      }),
    );

    expect(await within(link).findByText("Not linked to a user")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/people\/101\/user$/)[0].body).toEqual({ userId: null });
  });
});

describe("Person page — the care-day fields", () => {
  const withCareDays = (fetchMock: ReturnType<typeof stubStaffApi>) => {
    const ada = fetchMock.state.people.find((one) => one.id === 101);
    if (ada?.record) Object.assign(ada.record, { childrenUnder12: 2, soleCarer: true, chronicallyIllChildren: 1 });
  };

  it("shows HR the fields the person response carries", async () => {
    withCareDays(stubStaffApi({ role: "manage" }));
    renderRoute("/staff/people/101");

    const care = await region("Children and care days");
    expect(within(care).getByText("Children under 12").nextElementSibling).toHaveTextContent("2");
    expect(within(care).getByText("Sole carer").nextElementSibling).toHaveTextContent("Yes");
    expect(within(care).getByText("Children with extra care days").nextElementSibling).toHaveTextContent("1");
  });

  it("shows a colleague none of them, the response carrying none", async () => {
    withCareDays(stubStaffApi({ role: "view" }));
    renderRoute("/staff/people/101");

    await region("Work");
    expect(screen.queryByRole("region", { name: "Children and care days" })).not.toBeInTheDocument();
  });

  it("lets HR change them in the edit modal, an untouched one sent back as it was", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    withCareDays(fetchMock);
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    const children = within(dialog).getByRole("textbox", { name: "Children under 12" });
    expect(children).toHaveValue("2");
    await retype(children, "3");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/people\/101$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/people\/101$/);
    expect(put.body).toMatchObject({ childrenUnder12: 3, soleCarer: true, chronicallyIllChildren: 1 });
  });

  it("records them for a person who has none yet, and clears one emptied", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Edit details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit details" });
    await retype(within(dialog).getByRole("textbox", { name: "Children under 12" }), "1");
    await choose(dialog, "Sole carer", "No");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/people\/101$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/people\/101$/);
    expect(put.body).toMatchObject({ childrenUnder12: 1, soleCarer: false });
    expect(put.body).not.toHaveProperty("chronicallyIllChildren");
  });

  it("never sends them with my own details (PUT /me takes none)", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    withCareDays(fetchMock);
    renderRoute("/staff/me");

    await userEvent.click(await screen.findByRole("button", { name: "Edit my details" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit my details" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/me$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/me$/);
    for (const field of ["childrenUnder12", "soleCarer", "chronicallyIllChildren"])
      expect(put.body).not.toHaveProperty(field);
  });
});

/**
 * The slot (planning design §7, plan reading 3): the host adds a tab this
 * package never names — Planning's — after Account, addressable by URL; an
 * unknown tab, or the slot's tab when the host gives none, is the profile.
 */
describe("Person page — a tab the host adds", () => {
  const planningTab = { value: "planning", label: "Planning", render: () => <p>The person's plan</p> };

  it("keeps a lowercase tab the package does not know, and drops anything else and the profile", () => {
    expect(validatePersonSearch({ tab: "planning" })).toEqual({ tab: "planning" });
    expect(validatePersonSearch({ tab: "account" })).toEqual({ tab: "account" });
    for (const tab of ["profile", "Planning", "plan-ning", "x1", "", 3, undefined])
      expect(validatePersonSearch({ tab }), String(tab)).toEqual({ tab: undefined });
  });

  it("draws the host's tab after Account and renders it when the URL names it", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=planning", { personExtraTabs: [planningTab] });

    expect(await screen.findByText("The person's plan")).toBeInTheDocument();
    const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
    expect(tabs.at(-1)).toBe("Planning");
    expect(tabs.indexOf("Account")).toBe(tabs.length - 2);
    expect(screen.getByRole("tab", { name: "Planning" })).toHaveAttribute("aria-selected", "true");
  });

  it("opens the host's tab on a click and leaves it unrendered while another is current", async () => {
    stubStaffApi({ role: "manage" });
    const { router } = renderRoute("/staff/people/101", { personExtraTabs: [planningTab] });

    await region("Work");
    expect(screen.queryByText("The person's plan")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: "Planning" }));
    expect(await screen.findByText("The person's plan")).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ tab: "planning" });
  });

  it("falls back to the profile for a tab nobody draws", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=planning");

    expect(await region("Work")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Planning" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Profile" })).toHaveAttribute("aria-selected", "true");
  });
});

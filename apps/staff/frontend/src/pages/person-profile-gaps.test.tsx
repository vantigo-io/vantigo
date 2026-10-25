import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { competenceProfiles } from "../test/competence-fixtures";
import { peopleWithRecords } from "../test/fixtures";
import { choose, region, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

/** One item's row of a profile's table, by the competence it names. */
const itemRow = async (profile: HTMLElement, competence: string) =>
  (await within(profile).findByText(competence)).closest("tr") as HTMLElement;

describe("The Competence tab — what the profile asks", () => {
  it("heads my tab with every profile that reaches me, how, and each item's status", async () => {
    stubStaffApi({ role: "access", me: 102 });
    renderRoute("/staff/me?tab=competence");

    const group = await region("What your profile asks");
    // The tab opens with it: it comes before my competences.
    const competences = await region("Competences");
    expect(group.compareDocumentPosition(competences) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(group).queryByRole("button", { name: "Assign profiles" })).not.toBeInTheDocument();

    const electrician = await within(group).findByRole("region", { name: "Field electrician" });
    expect(electrician).toHaveTextContent("Assigned");
    expect(electrician).toHaveTextContent("2 not met");
    const welding = await itemRow(electrician, "Welding");
    expect(welding).toHaveTextContent("At least experienced");
    expect(welding).toHaveTextContent("Missing");

    const operator = await within(group).findByRole("region", { name: "Operator" });
    expect(operator).toHaveTextContent("Default for Operations (OPS)");
    expect(operator).not.toHaveTextContent("Assigned");
    expect(operator).toHaveTextContent("1 not met");
    const fse = await itemRow(operator, "Electrical safety (FSE)");
    expect(fse).toHaveTextContent("Valid");
    expect(fse).toHaveTextContent("Missing");
    // A skill declared counts as met, with the badge (plan reading 23).
    const english = await itemRow(operator, "English");
    expect(english).toHaveTextContent("At least independent");
    expect(within(english).getByText("Met")).toBeInTheDocument();
    expect(within(english).getByText("Declared")).toBeInTheDocument();
    expect(english).toHaveTextContent("experienced");
  });

  it("says no profile asks anything of a person no profile reaches", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const group = await region("What your profile asks");
    expect(await within(group).findByText("No profile asks anything of you")).toBeInTheDocument();
  });

  it("words an expiring certificate and one declared but not verified, and leaves an archived profile out", async () => {
    const profiles = competenceProfiles();
    profiles[0].archivedAt = "2026-10-01T08:00:00Z";
    stubStaffApi({
      role: "manage",
      competenceProfiles: profiles,
      // Ada is an operator too: her FSE expires within its lead, and she holds no English.
      personProfiles: [
        { personId: 101, profileId: 1002 },
        { personId: 101, profileId: 1001 },
      ],
    });
    renderRoute("/staff/people/101?tab=competence");

    const group = await region("What their profile asks");
    const operator = await within(group).findByRole("region", { name: "Operator" });
    const fse = await itemRow(operator, "Electrical safety (FSE)");
    expect(fse).toHaveTextContent("Expires soon");
    expect(fse).toHaveTextContent("Valid to Nov 1, 2026");
    expect(await itemRow(operator, "English")).toHaveTextContent("Missing");
    // Expiring is met still: only English counts.
    expect(operator).toHaveTextContent("1 not met");
    expect(within(group).queryByRole("region", { name: /Field electrician/ })).not.toBeInTheDocument();
  });

  it("lets HR assign profiles, the list replaced whole without If-Match", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    await userEvent.click(within(group).getByRole("button", { name: "Assign profiles" }));
    const dialog = await screen.findByRole("dialog", { name: "Assign profiles to Grace Hopper" });
    await choose(dialog, "Competence profiles", "Operator");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save profiles" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/people\/102\/competence-profiles$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/people\/102\/competence-profiles$/);
    expect(put.body).toEqual({ profileIds: [1001, 1002] });
    expect(put.ifMatch).toBeNull();
    // Assigned and the department's default, both said.
    const operator = await within(group).findByRole("region", { name: "Operator" });
    await waitFor(() => expect(operator).toHaveTextContent("Assigned · Default for Operations (OPS)"));
  });

  it("puts a refusal at an index on the banner, naming the profile", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/competence-profiles")
          ? staffProblem(400, {
              title: "Invalid profile assignment",
              errors: { "profileIds[1]": ["The profile is archived"] },
            })
          : undefined,
    });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    await userEvent.click(within(group).getByRole("button", { name: "Assign profiles" }));
    const dialog = await screen.findByRole("dialog", { name: "Assign profiles to Grace Hopper" });
    await choose(dialog, "Competence profiles", "Operator");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save profiles" }));

    expect(await within(dialog).findByText("Operator: The profile is archived")).toBeInTheDocument();
  });

  it("draws nothing for a colleague, whom the gap list answers with the 404, asked once", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    const { queryClient } = renderRoute("/staff/people/102?tab=competence");

    await region("Competences");
    const key = ["staff", "people", "detail", 102, "competence-gaps"];
    await waitFor(() => expect(queryClient.getQueryState(key)?.status).toBe("error"));
    // The 404 settles the query at once: no retry was waiting, and none follows.
    expect(queryClient.getQueryState(key)?.fetchFailureCount).toBe(1);
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(sent(fetchMock, "GET", /\/people\/102\/competence-gaps$/)).toHaveLength(1);
    expect(screen.queryByRole("region", { name: "What their profile asks" })).not.toBeInTheDocument();
    expect(screen.queryByText("Could not load what the profiles ask")).not.toBeInTheDocument();
  });

  it("shows the manager who is not HR what the profile asks, with nothing to assign", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    expect(await within(group).findByRole("region", { name: "Operator" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Assign profiles" })).not.toBeInTheDocument();
  });

  it("shows staff:personal-data alone what the profile asks, with nothing to assign", async () => {
    stubStaffApi({ role: "personalDataOnly" });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    expect(await within(group).findByRole("region", { name: "Field electrician" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Assign profiles" })).not.toBeInTheDocument();
  });

  it("answers someone with no link to the person the unknown person's 404", async () => {
    stubStaffApi({ role: "access", me: 102 });
    for (const kind of ["competence-gaps", "competence-profiles"])
      expect((await fetch(`http://localhost/api/v1/staff/people/101/${kind}`)).status).toBe(404);
  });

  it("still shows an erased person's profiles, with nothing to assign", async () => {
    stubStaffApi({
      role: "manage",
      people: peopleWithRecords().map((one) => (one.id === 102 ? { ...one, erasedAt: "2026-10-01T02:00:00Z" } : one)),
    });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    expect(await within(group).findByRole("region", { name: "Operator" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Assign profiles" })).not.toBeInTheDocument();
  });

  it("offers an archived profile to assign only when it is assigned already", async () => {
    const profiles = competenceProfiles().map((one) => ({ ...one, archivedAt: "2026-10-01T08:00:00Z" }));
    stubStaffApi({ role: "manage", competenceProfiles: profiles });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    await userEvent.click(within(group).getByRole("button", { name: "Assign profiles" }));
    const dialog = await screen.findByRole("dialog", { name: "Assign profiles to Grace Hopper" });
    // Grace is assigned the field electrician, archived since; the operator reaches her by her department alone.
    expect((await within(dialog).findAllByText("Field electrician (archived)")).length).toBeGreaterThan(0);
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Competence profiles" }));
    expect(await screen.findByRole("option", { name: "Field electrician (archived)" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Operator/ })).not.toBeInTheDocument();
  });

  // Plan reading 23: a row without a level is never below the minimum (gapStatus).
  it("judges a skill held without a level as met against a minimum level", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    const english = fetchMock.state.personCompetences.find(
      (one) => one.personId === 102 && one.competence.id === 2005 && !one.superseded,
    );
    expect(english).toBeDefined();
    if (english) delete english.level;

    const gaps = await (await fetch("http://localhost/api/v1/staff/people/102/competence-gaps")).json();
    const operator = gaps.profiles.find((one: { profile: { id: number } }) => one.profile.id === 1002);
    const item = operator.items.find((one: { competenceId: number }) => one.competenceId === 2005);
    expect(item.minLevel).toBe(2);
    expect(item.status).toBe("met");
    expect(item.level).toBeUndefined();
  });

  it("shows an Approver what the profile asks, with nothing to assign", async () => {
    stubStaffApi({ role: "approve" });
    renderRoute("/staff/people/102?tab=competence");

    const group = await region("What their profile asks");
    expect(await within(group).findByRole("region", { name: "Operator" })).toBeInTheDocument();
    expect(within(group).queryByRole("button", { name: "Assign profiles" })).not.toBeInTheDocument();
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { StaffCompetenceProfile } from "../api/competences";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const profileRow = async (key: string) => {
  const table = await screen.findByRole("table", { name: "Competence profiles" });
  return (await within(table).findByText(key)).closest("tr") as HTMLElement;
};

describe("Settings — the competence profiles", () => {
  it("lists every profile with its items, the archived marked", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    fetchMock.state.competenceProfiles[1].archivedAt = "2026-10-01T08:00:00Z";
    renderRoute("/staff/settings");

    const electrician = await profileRow("field-electrician");
    expect(electrician).toHaveTextContent("Field electrician");
    expect(electrician).toHaveTextContent("Feltelektriker");
    expect(electrician).toHaveTextContent("2");
    expect(electrician).not.toHaveTextContent("Archived");
    expect(await profileRow("operator")).toHaveTextContent("Archived");
  });

  it("adds a profile: a level for a competence that has one, must be valid for a certificate", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    const card = await region("Competence profiles");
    await userEvent.click(within(card).getByRole("button", { name: "New profile" }));
    const dialog = await screen.findByRole("dialog", { name: "New profile" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Key/ }), "site-lead");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name in English/ }), "Site lead");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name in Norwegian/ }), "Anleggsleder");

    await userEvent.click(within(dialog).getByRole("button", { name: "Add item" }));
    await choose(dialog, "Competence", "Welding");
    await choose(dialog, "Minimum level", "experienced");
    // A skill has no validity: no switch to say it must be valid.
    expect(within(dialog).queryByRole("switch", { name: "Must be valid" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Add item" }));
    await choose(dialog, "Competence", "Electrical safety (FSE)", 1);
    // A certificate takes no level; it must be valid unless told otherwise.
    expect(within(dialog).getAllByRole("combobox", { name: "Minimum level" })).toHaveLength(1);
    expect(within(dialog).getByRole("switch", { name: "Must be valid" })).toBeChecked();
    // Neither a hidden nor an archived competence is offered for a new item.
    await userEvent.click(within(dialog).getAllByRole("combobox", { name: "Competence" })[1]);
    expect(screen.queryByRole("option", { name: "First aid" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Lotus/ })).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/competence-profiles$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/competence-profiles$/)[0].body).toEqual({
      key: "site-lead",
      names: { en: "Site lead", nb: "Anleggsleder" },
      items: [
        { competenceId: 2004, minLevel: 3, mustBeValid: true },
        { competenceId: 2001, mustBeValid: true },
      ],
    });
    expect(await profileRow("site-lead")).toHaveTextContent("Site lead");
  });

  it("changes a profile whole under its revision, and archives it", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(within(await profileRow("operator")).getByRole("button", { name: "Change Operator" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Operator" });
    expect(within(dialog).getByText("operator")).toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: /^Key/ })).not.toBeInTheDocument();
    await retype(within(dialog).getByRole("textbox", { name: /^Name in English/ }), "Machine operator");
    await userEvent.click(within(dialog).getByRole("switch", { name: /^Must be valid/ }));
    await userEvent.click(within(dialog).getByRole("switch", { name: /^Archived/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/competence-profiles\/1002$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/competence-profiles\/1002$/);
    expect(put.ifMatch).toBe("2");
    expect(put.body).toEqual({
      names: { en: "Machine operator", nb: "Operatør" },
      archived: true,
      items: [
        { competenceId: 2001, mustBeValid: false },
        { competenceId: 2005, minLevel: 2, mustBeValid: true },
      ],
    });
    expect(await profileRow("operator")).toHaveTextContent("Archived");
  });

  it("removes an item", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await profileRow("field-electrician")).getByRole("button", { name: "Change Field electrician" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Change Field electrician" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove item 1" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/competence-profiles\/1001$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/competence-profiles\/1001$/)[0].body.items).toEqual([
      { competenceId: 2001, mustBeValid: true },
    ]);
  });

  it("puts a level refused on a competence without one on the item's level, in the catalog's words", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    // The operator asks Excel at a level, which Excel does not take: the server refuses it as it stands.
    const operator = fetchMock.state.competenceProfiles.find((one) => one.id === 1002) as StaffCompetenceProfile;
    operator.items[1] = {
      competenceId: 2006,
      competence: {
        id: 2006,
        key: "excel",
        names: { en: "Excel", nb: "Excel" },
        kind: "skill",
        hasLevel: false,
        categoryId: 1004,
        archived: false,
      },
      minLevel: 1,
      mustBeValid: true,
    };
    renderRoute("/staff/settings");

    await userEvent.click(within(await profileRow("operator")).getByRole("button", { name: "Change Operator" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Operator" });
    // The level is shown to be cleared, though Excel takes none.
    const level = await within(dialog).findByRole("combobox", { name: "Minimum level" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("This competence takes no level.")).toBeInTheDocument();
    expect(level).toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).queryByText("Could not save")).not.toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/competence-profiles\/1002$/)[0].body.items[1]).toEqual({
      competenceId: 2006,
      minLevel: 1,
      mustBeValid: true,
    });
  });

  it("puts a competence refused as unavailable on that item's competence, and no other", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/competence-profiles/1002")
          ? staffProblem(400, {
              code: "competence_unavailable",
              title: "Invalid competence profile",
              errors: {
                "items[1].competenceId": [
                  "Competence 2005 is hidden or archived; a new or changed item cannot name it",
                ],
              },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(within(await profileRow("operator")).getByRole("button", { name: "Change Operator" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Operator" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("This competence is no longer offered.")).toBeInTheDocument();
    const [first, second] = within(dialog).getAllByRole("combobox", { name: "Competence" });
    expect(second).toHaveAttribute("aria-invalid", "true");
    expect(first).not.toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).getByRole("combobox", { name: "Minimum level" })).not.toHaveAttribute("aria-invalid", "true");
    expect(within(dialog).queryByText("Could not save")).not.toBeInTheDocument();
  });

  it("reloads on a revision conflict and says so", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/competence-profiles/1002")
          ? staffProblem(409, { code: "revision_conflict", title: "Revision conflict", detail: "Stale" })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(within(await profileRow("operator")).getByRole("button", { name: "Change Operator" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Operator" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
  });

  it("is not drawn for a reader who edits nothing", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/settings");

    await screen.findByRole("table", { name: "Departments" });
    expect(screen.queryByRole("region", { name: "Competence profiles" })).not.toBeInTheDocument();
  });
});

describe("The fake server's profile writes, judged in the server's order", () => {
  const put = (id: number, body: unknown, ifMatch: string | null = "2") =>
    fetch(`http://localhost/api/v1/staff/competence-profiles/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", ...(ifMatch === null ? {} : { "If-Match": ifMatch }) },
      body: JSON.stringify(body),
    });
  const operator = (items: unknown[], names = { en: "Operator", nb: "Operatør" }) => ({
    names,
    archived: false,
    items,
  });

  it("refuses an unknown competence as unavailable, on its index", async () => {
    stubStaffApi({ role: "manage" });
    const answer = await put(1002, operator([{ competenceId: 2001 }, { competenceId: 9999 }]));
    expect(answer.status).toBe(400);
    const problem = await answer.json();
    expect(problem.code).toBe("competence_unavailable");
    expect(Object.keys(problem.errors)).toEqual(["items[1].competenceId"]);
  });

  it("refuses a hidden competence without a level asked at one on both rules of the same index", async () => {
    stubStaffApi({ role: "manage" });
    const answer = await put(1002, operator([{ competenceId: 2003, minLevel: 2 }]));
    const problem = await answer.json();
    expect(problem.code).toBe("competence_unavailable");
    expect(Object.keys(problem.errors).sort()).toEqual(["items[0].competenceId", "items[0].minLevel"]);
  });

  it("asks for If-Match first, judges the body on its own before the 404, and the revision before the catalogue", async () => {
    stubStaffApi({ role: "manage" });
    expect((await put(9999, operator([]), null)).status).toBe(428);
    const unnamed = await put(9999, operator([], { en: " ", nb: "Operatør" }));
    expect(unnamed.status).toBe(400);
    expect((await unnamed.json()).errors).toEqual({ "names.en": ["An English name is required"] });
    expect((await put(9999, operator([]))).status).toBe(404);
    expect((await put(1002, operator([{ competenceId: 9999 }]), "1")).status).toBe(409);
  });

  it("refuses an unknown competence in the matrix on competenceId", async () => {
    stubStaffApi({ role: "manage" });
    const answer = await fetch("http://localhost/api/v1/staff/competence-matrix?competenceId=2001&competenceId=9999");
    expect(answer.status).toBe(400);
    expect((await answer.json()).errors).toEqual({ competenceId: ["No competence has id 9999"] });
  });
});

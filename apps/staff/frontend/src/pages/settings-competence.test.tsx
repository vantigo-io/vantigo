import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const tableRow = async (table: string, text: string) => {
  const found = await screen.findByRole("table", { name: table });
  return (await within(found).findAllByText(text))[0].closest("tr") as HTMLElement;
};

describe("Settings — competence categories", () => {
  it("shows the seeds with no action, and adds a category of the company's own", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    const fag = await tableRow("Competence categories", "fag");
    expect(fag).toHaveTextContent("Trade");
    expect(fag).toHaveTextContent("Seeded");
    expect(within(fag).queryAllByRole("button")).toHaveLength(0);
    expect(
      within(await tableRow("Competence categories", "safety")).getByRole("button", { name: "Archive Safety" }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Add competence category" }));
    const dialog = await screen.findByRole("dialog", { name: "New competence category" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Key/ }), "lifting");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^English name/ }), "Lifting");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Norwegian name/ }), "Løft");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/competence-categories$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/competence-categories$/)[0].body).toEqual({
      key: "lifting",
      names: { en: "Lifting", nb: "Løft" },
    });
    expect(await tableRow("Competence categories", "lifting")).toHaveTextContent("Løft");
  });
});

describe("Settings — the competence catalogue", () => {
  it("lists the hidden and the archived to HR, each with its badge", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    const fse = await tableRow("Competences", "Electrical safety (FSE)");
    expect(fse).toHaveTextContent("Certificate");
    expect(fse).toHaveTextContent("Seeded");
    expect(fse).toHaveTextContent("The employer, by FSE § 7");
    // A seed is hidden, never archived.
    expect(within(fse).queryByRole("button", { name: /^Archive/ })).not.toBeInTheDocument();
    expect(await tableRow("Competences", "First aid")).toHaveTextContent("Hidden");
    expect(await tableRow("Competences", "Lotus 1-2-3")).toHaveTextContent("Archived");
    expect(sent(fetchMock, "GET", /\/competences\?includeHidden=true&includeArchived=true$/).length).toBeGreaterThan(0);
  });

  it("adds a certificate with its validity", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "New competence" }));
    const dialog = await screen.findByRole("dialog", { name: "New competence" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Key/ }), "forklift");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name in English/ }), "Forklift");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name in Norwegian/ }), "Truck");
    await choose(dialog, /^Category/, "Safety");
    await choose(dialog, /^Kind/, "Certificate");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Validity \(months\)/ }), "60");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/competences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/competences$/)[0].body).toEqual({
      categoryId: 1006,
      key: "forklift",
      names: { en: "Forklift", nb: "Truck" },
      kind: "certificate",
      hasLevel: false,
      validityMonths: 60,
    });
    // The server gave it the lead for five years.
    expect(await tableRow("Competences", "Forklift")).toHaveTextContent("180");
  });

  it("hides a seed with every other field sent as it was", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Competences", "Hot work")).getByRole("button", { name: "Hide Hot work" }),
    );
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/competences\/2002$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/competences\/2002$/);
    expect(put.ifMatch).toBe("1");
    expect(put.body).toEqual({
      categoryId: 1002,
      names: { en: "Hot work", nb: "Varme arbeider" },
      hasLevel: false,
      hidden: true,
      archived: false,
      validityMonths: 60,
      reminderLeadDays: 180,
    });
    expect(await within(await tableRow("Competences", "Hot work")).findByText("Hidden")).toBeInTheDocument();
  });

  it("archives a company competence", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Competences", "Excel")).getByRole("button", { name: "Archive Excel" }),
    );
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/competences\/2006$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/competences\/2006$/)[0].body).toMatchObject({ archived: true, hidden: false });
    expect(
      await within(await tableRow("Competences", "Excel")).findByRole("button", { name: "Restore Excel" }),
    ).toBeInTheDocument();
  });

  it("says seed_immutable in words", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/competences/2001")
          ? staffProblem(409, { code: "seed_immutable", title: "Seed", detail: "English" })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Competences", "Electrical safety (FSE)")).getByRole("button", {
        name: "Hide Electrical safety (FSE)",
      }),
    );
    expect(
      await screen.findByText(
        "A seeded competence changes only its names, the reminder, the issuer and whether it is hidden.",
      ),
    ).toBeInTheDocument();
  });

  it("shows a seed's fixed fields disabled, with the reason", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Change Electrical safety (FSE)" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Electrical safety (FSE)" });
    expect(within(dialog).getByText(/A seeded competence keeps its key, kind, level and validity/)).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: /^Name in English/ })).toBeEnabled();
    expect(within(dialog).getByRole("switch", { name: /^Has a level/ })).toBeDisabled();
    expect(within(dialog).getByRole("textbox", { name: /^Validity \(months\)/ })).toBeDisabled();
    expect(within(dialog).getByRole("textbox", { name: /^Reminder/ })).toBeEnabled();
  });
});

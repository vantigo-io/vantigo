import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { allAbsenceTypes } from "../test/absence-fixtures";
import { absenceTypes, holidaySchemes, settings } from "../test/fixtures";
import { choose, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const tableRow = async (table: string, name: string) => {
  const found = await screen.findByRole("table", { name: table });
  return (await within(found).findAllByText(name))[0].closest("tr") as HTMLElement;
};

const agreement = () => ({
  ...holidaySchemes()[0],
  id: 1002,
  name: "Tariff (five weeks)",
  entitlementVirkedager: 30,
  maxCarryOverVirkedager: 17,
  converted: { entitlement: 25, extraFrom60: 5, mainPeriod: 15, afterSeptember: 5, maxCarryOver: 15 },
  isDefault: false,
  seeded: false,
  revision: 3,
});

describe("Settings — absence types", () => {
  it("shows the seeds read-only with their a-melding mapping and visibility", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    const pregnancy = await tableRow("Absence types", "Pregnancy leave");
    expect(pregnancy).toHaveTextContent("HR only");
    expect(pregnancy).toHaveTextContent("permisjonMedForeldrepenger");
    expect(pregnancy).toHaveTextContent("Seeded");
    expect(await tableRow("Absence types", "Welfare leave")).toHaveTextContent("Hidden");
    // The reserved type is never shown, offered nor edited.
    expect(within(await screen.findByRole("table", { name: "Absence types" })).queryByText("Absent")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Change Pregnancy leave" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Pregnancy leave" });
    expect(within(dialog).getByRole("textbox", { name: "Name in English" })).toBeEnabled();
    expect(within(dialog).getByRole("switch", { name: "Needs a decision" })).toBeDisabled();
    expect(within(dialog).getByRole("combobox", { name: "Who sees the type" })).toBeDisabled();
  });

  it("hides a seed with every other field sent as it was", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Absence types", "Holiday")).getByRole("button", { name: "Hide Holiday" }),
    );
    await waitFor(() => expect(sent(fetchMock, "PUT", /absence-types\/1001$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /absence-types\/1001$/);
    expect(put.ifMatch).toBe("1");
    expect(put.body).toEqual({
      names: { en: "Holiday", nb: "Ferie" },
      sort: 1,
      hidden: true,
      allowsPercent: false,
      drawsHoliday: true,
      needsDecision: true,
      paid: true,
      visibility: "absent",
    });
    expect(await tableRow("Absence types", "Holiday")).toHaveTextContent("Hidden");
  });

  it("says seed_immutable in words", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/absence-types/1001")
          ? staffProblem(409, { code: "seed_immutable", title: "Seed", detail: "English" })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Change Holiday" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Holiday" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(dialog).findByText(/A seeded type changes only its names, its order/)).toBeInTheDocument();
  });

  it("edits a company type, keeping what it does not touch, and says absence_type_in_use in words", async () => {
    const fetchMock = stubStaffApi({
      role: "manage",
      write: (method, path, body) =>
        method === "PUT" &&
        path.endsWith("/absence-types/2001") &&
        (body as { visibility?: string }).visibility === "absent"
          ? staffProblem(409, { code: "absence_type_in_use", title: "In use", detail: "English" })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Change Study day" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Study day" });
    await retype(within(dialog).getByRole("textbox", { name: "Name in English" }), "Study days");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /absence-types\/2001$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /absence-types\/2001$/)[0].body).toEqual({
      names: { en: "Study days", nb: "Studiedag" },
      sort: 30,
      hidden: false,
      allowsPercent: true,
      drawsHoliday: false,
      needsDecision: true,
      paid: true,
      visibility: "hr_only",
    });
    expect(sent(fetchMock, "PUT", /absence-types\/2001$/)[0].ifMatch).toBe("2");

    await userEvent.click(await screen.findByRole("button", { name: "Change Study days" }));
    const again = await screen.findByRole("dialog", { name: "Change Study days" });
    await choose(again, "Who sees the type", /Colleagues see/);
    await userEvent.click(within(again).getByRole("button", { name: "Save" }));
    expect(await within(again).findByText(/while an absence names it/)).toBeInTheDocument();
  });

  it("clears a company type's a-melding mapping, sending none", async () => {
    const types = allAbsenceTypes(absenceTypes()).map((one) =>
      one.id === 2001 ? { ...one, ameldingLeave: "andreIkkeLovfestedePermisjoner" as const } : one,
    );
    const fetchMock = stubStaffApi({ role: "manage", absence: { types } });
    renderRoute("/staff/settings");

    expect(await tableRow("Absence types", "Study day")).toHaveTextContent("andreIkkeLovfestedePermisjoner");
    await userEvent.click(await screen.findByRole("button", { name: "Change Study day" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Study day" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Clear the a-melding description" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /absence-types\/2001$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /absence-types\/2001$/)[0].body).not.toHaveProperty("ameldingLeave");
    await waitFor(async () =>
      expect(await tableRow("Absence types", "Study day")).not.toHaveTextContent("andreIkkeLovfestedePermisjoner"),
    );
  });

  it("adds a company type", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "New absence type" }));
    const dialog = await screen.findByRole("dialog", { name: "New absence type" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Key" }), "kurs");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name in English" }), "Course");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name in Norwegian" }), "Kurs");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /absence-types$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /absence-types$/)[0].body).toEqual({
      key: "kurs",
      names: { en: "Course", nb: "Kurs" },
      kind: "leave",
      unit: "days",
      paid: true,
      drawsHoliday: false,
      allowsPercent: false,
      needsDecision: true,
      visibility: "absent",
    });
  });
});

describe("Settings — holiday schemes", () => {
  it("shows each scheme with its conversion and the default", async () => {
    stubStaffApi({ role: "manage", absence: { schemes: [...holidaySchemes(), agreement()] } });
    renderRoute("/staff/settings");

    const statutory = await tableRow("Holiday schemes", "Statutory (ferieloven)");
    expect(statutory).toHaveTextContent("25 virkedager = 21 days on a five-day week");
    expect(statutory).toHaveTextContent("Default");
    expect(await tableRow("Holiday schemes", "Tariff (five weeks)")).toHaveTextContent(
      "30 virkedager = 25 days on a five-day week",
    );
  });

  it("keeps every figure it does not touch on a save", async () => {
    const fetchMock = stubStaffApi({ role: "manage", absence: { schemes: [...holidaySchemes(), agreement()] } });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Change Tariff (five weeks)" }));
    const dialog = await screen.findByRole("dialog", { name: "Change Tariff (five weeks)" });
    await retype(within(dialog).getByRole("textbox", { name: "Name" }), "Tariff");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /holiday-schemes\/1002$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /holiday-schemes\/1002$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      name: "Tariff",
      entitlementVirkedager: 30,
      extraFrom60Virkedager: 6,
      mainPeriodVirkedager: 18,
      afterSeptemberVirkedager: 6,
      maxCarryOverVirkedager: 17,
      archived: false,
    });
  });

  it("holds the statutory floors in the form too", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "New holiday scheme" }));
    const dialog = await screen.findByRole("dialog", { name: "New holiday scheme" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Short");
    await retype(within(dialog).getByRole("textbox", { name: "Main period (virkedager)" }), "17");
    await retype(within(dialog).getByRole("textbox", { name: "After September (virkedager)" }), "5");
    await retype(within(dialog).getByRole("textbox", { name: "Most carried over (virkedager)" }), "13");
    expect(within(dialog).getByText("= 21 days on a five-day week")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await within(dialog).findByText("18 to 25 (ferieloven § 7)")).toBeInTheDocument();
    expect(within(dialog).getByText("6 to 25 (ferieloven § 5)")).toBeInTheDocument();
    expect(within(dialog).getByText("0 to 12 (ferieloven § 7)")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /holiday-schemes$/)).toHaveLength(0);

    // A longer entitlement lets more be carried over: 12 plus what is above 25.
    await retype(within(dialog).getByRole("textbox", { name: "Entitlement (virkedager)" }), "30");
    await retype(within(dialog).getByRole("textbox", { name: "Main period (virkedager)" }), "18");
    await retype(within(dialog).getByRole("textbox", { name: "After September (virkedager)" }), "6");
    await retype(within(dialog).getByRole("textbox", { name: "Most carried over (virkedager)" }), "17");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /holiday-schemes$/)).toHaveLength(1));
  });

  it("says holiday_scheme_in_use in words when the default is archived", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Archive Statutory (ferieloven)" }));
    expect(
      await screen.findByText(/The default scheme, or one an employment follows, is not archived/),
    ).toBeInTheDocument();
  });
});

describe("Settings — the self-certification policy and carry-over", () => {
  it("saves custom numbers with every other setting sent back as it was", async () => {
    const fetchMock = stubStaffApi({ role: "manage", settings: settings({ injuryInsurer: "If" }) });
    renderRoute("/staff/settings");

    const card = await screen.findByRole("region", { name: "Egenmelding and carry-over" });
    await choose(card, "Policy", /Custom/);
    await retype(within(card).getByRole("textbox", { name: "Days per occurrence" }), "5");
    await retype(within(card).getByRole("textbox", { name: "Occurrences per year" }), "6");
    await retype(within(card).getByRole("textbox", { name: "Days per year" }), "20");
    await userEvent.click(within(card).getByRole("switch", { name: /^Carry untaken holiday over automatically/ }));
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/settings$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/settings$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      timeZone: "Europe/Oslo",
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      injuryInsurer: "If",
      selfCertPolicy: "custom",
      selfCertDaysPerOccurrence: 5,
      selfCertOccurrencesPerYear: 6,
      selfCertDaysPerYear: 20,
      selfCertAfterMonths: 2,
      autoCarryOver: true,
    });
  });

  it("holds ftrl. § 8-23's floors on custom numbers", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/settings");

    const card = await screen.findByRole("region", { name: "Egenmelding and carry-over" });
    await choose(card, "Policy", /Custom/);
    await retype(within(card).getByRole("textbox", { name: "Days per occurrence" }), "2");
    await retype(within(card).getByRole("textbox", { name: "Occurrences per year" }), "3");
    await retype(within(card).getByRole("textbox", { name: "Days per year" }), "11");
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    expect(await within(card).findByText("3 to 16 days (ftrl. § 8-23)")).toBeInTheDocument();
    expect(within(card).getByText("4 to 52 (ftrl. § 8-23)")).toBeInTheDocument();
    expect(within(card).getByText("12 to 365 days (ftrl. § 8-23)")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/settings$/)).toHaveLength(0);
  });

  it("sends a preset without numbers, which the preset writes itself", async () => {
    const fetchMock = stubStaffApi({
      role: "manage",
      settings: settings({ selfCertPolicy: "custom", selfCertDaysPerOccurrence: 5 }),
    });
    renderRoute("/staff/settings");

    const card = await screen.findByRole("region", { name: "Egenmelding and carry-over" });
    await choose(card, "Policy", /Extended/);
    expect(within(card).getByText(/8 days at a time, 24 days a year/)).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/settings$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/settings$/);
    expect(put.body).toMatchObject({ selfCertPolicy: "extended", selfCertAfterMonths: 2, autoCarryOver: false });
    for (const field of ["selfCertDaysPerOccurrence", "selfCertOccurrencesPerYear", "selfCertDaysPerYear"])
      expect(put.body).not.toHaveProperty(field);
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { employments, terms } from "../test/fixtures";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

const adaEmployment = () => region("Employment E-1-1");

/** The terms rows of a card, top to bottom, by their names. */
const rowNames = (card: HTMLElement) =>
  within(card)
    .getAllByRole("region", { name: /^Terms from/ })
    .map((row) => row.getAttribute("aria-label"));

/** Types a calendar day into a date field the way a person does, and leaves it. */
const typeDate = async (field: HTMLElement, text: string) => {
  await userEvent.clear(field);
  await userEvent.type(field, text);
  await userEvent.tab();
};

describe("Employments — the card and the terms timeline", () => {
  it("shows HR each employment with its terms newest first, the pay and the note", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    const card = await adaEmployment();
    expect(within(card).getByText("Jan 1, 2025")).toBeInTheDocument();
    expect(within(card).getByText("Oslo office")).toBeInTheDocument();
    expect(within(card).getByText("Active")).toBeInTheDocument();
    await waitFor(() =>
      expect(rowNames(card)).toEqual(["Terms from Mar 1, 2026", "Terms from Jan 1, 2025 to Feb 28, 2026"]),
    );
    const latest = await region("Terms from Mar 1, 2026");
    expect(within(latest).getByText("In force")).toBeInTheDocument();
    expect(within(latest).getByText("Senior engineer")).toBeInTheDocument();
    expect(within(latest).getByText(/Base salary/)).toHaveTextContent(/700,000/);
    const first = await region("Terms from Jan 1, 2025 to Feb 28, 2026");
    expect(within(first).getByText("Hired through the referral programme")).toBeInTheDocument();
  });

  it("shows a manager the non-pay terms of their report, and no pay", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=employments");

    const row = await region("Terms from Jan 15, 2026");
    expect(within(row).getByText("Operator")).toBeInTheDocument();
    expect(within(row).queryByText(/Hourly wage/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New terms" })).not.toBeInTheDocument();
    expect(await region("Contract check")).toBeInTheDocument();
    // The manager holds no structure: the unit and the location are named
    // from the person's place today.
    const card = await region("Employment E-2-1");
    expect(within(card).getByText("Bergen office")).toBeInTheDocument();
    expect(within(row).getByText("Bergen")).toBeInTheDocument();
  });

  it("shows no pay to a reader who may not see it, whatever the response carries", async () => {
    const full = { ...terms().find((one) => one.id === 303), payMethod: "Bank transfer", payGrade: "L3" };
    const employment = employments().find((one) => one.id === 202);
    stubStaffApi({
      role: "access",
      me: 101,
      read: (path) =>
        path === "/api/v1/staff/employments/202/terms"
          ? jsonResponse(200, [full])
          : path === "/api/v1/staff/people/102/employments"
            ? jsonResponse(200, [employment])
            : undefined,
    });
    renderRoute("/staff/people/102?tab=employments");

    const row = await region("Terms from Jan 15, 2026");
    expect(within(row).getByText("Operator")).toBeInTheDocument();
    for (const text of [/Hourly wage/, "Bank transfer", "L3"])
      expect(within(row).queryByText(text)).not.toBeInTheDocument();
    expect(screen.queryByText("Pay in force since")).not.toBeInTheDocument();
  });

  it("shows a colleague the dates, the type and the percentage — no title, pay, check or actions", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people/101?tab=employments");

    const row = await region("Terms from Mar 1, 2026");
    expect(within(row).getByText("80 %")).toBeInTheDocument();
    expect(within(row).getByText("Permanent (fast)")).toBeInTheDocument();
    expect(within(row).queryByText("Senior engineer")).not.toBeInTheDocument();
    expect(within(row).queryByText(/Base salary/)).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Contract check" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Correct|Delete|End employment|New terms/ })).not.toBeInTheDocument();
  });
});

describe("Employments — the row in force lies within the employment", () => {
  it("marks no row in force once the employment has ended", async () => {
    stubStaffApi({
      role: "manage",
      employments: employments().map((one) =>
        one.id === 201 ? { ...one, endDate: "2026-05-31", endReason: "employee_resigned" as const } : one,
      ),
    });
    renderRoute("/staff/people/101?tab=employments");

    const card = await adaEmployment();
    await within(card).findByRole("region", { name: "Terms from Mar 1, 2026" });
    expect(within(card).queryByText("In force")).not.toBeInTheDocument();
  });

  it("marks no row in force before the employment starts", async () => {
    stubStaffApi({
      role: "manage",
      employments: employments().map((one) => (one.id === 202 ? { ...one, startDate: "2026-11-01" } : one)),
      terms: undefined,
    });
    renderRoute("/staff/people/102?tab=employments");

    const card = await region("Employment E-2-1");
    // The row is dated the old start, before today — but the employment has not begun.
    await within(card).findByRole("region", { name: "Terms from Jan 15, 2026" });
    expect(within(card).queryByText("In force")).not.toBeInTheDocument();
  });
});

describe("Employments — HR changes the terms", () => {
  it("prefills New terms from the row in force and sends the whole row on its day", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "New terms" }));
    const dialog = await screen.findByRole("dialog", { name: "New terms" });
    expect(within(dialog).getByRole("textbox", { name: /Percentage of a full position/ })).toHaveValue("80");
    expect(within(dialog).getByRole("textbox", { name: "Title" })).toHaveValue("Senior engineer");
    expect(within(dialog).getAllByRole("textbox", { name: "Name" })[0]).toHaveValue("Base salary");

    await typeDate(within(dialog).getByRole("textbox", { name: /Takes effect/ }), "Apr 1, 2027");
    await retype(within(dialog).getByRole("textbox", { name: /Percentage of a full position/ }), "90");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save terms" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /employments\/201\/terms$/)).toHaveLength(1));
    const [post] = sent(fetchMock, "POST", /employments\/201\/terms$/);
    expect(post.body).toMatchObject({
      validFrom: "2027-04-01",
      employmentType: "fast",
      ftePercent: 90,
      title: "Senior engineer",
      noticeUnit: "months",
      payComponents: [{ name: "Base salary", amount: 700000, currency: "NOK", period: "yearly" }],
      seniorityDate: "2020-01-01",
      payGrade: "L7",
    });
    // A field the row before had and the form emptied is cleared, not inherited.
    expect(post.body.workingTimeText).toBeNull();
    expect(await region("Terms from Apr 1, 2027")).toBeInTheDocument();
  });

  it("puts a day another row has on the day's field", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "New terms" }));
    const dialog = await screen.findByRole("dialog", { name: "New terms" });
    await typeDate(within(dialog).getByRole("textbox", { name: /Takes effect/ }), "Mar 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save terms" }));

    expect(
      await within(dialog).findByText("Another terms row of this employment takes effect on 2026-03-01."),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: /Takes effect/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("puts the server's manager cycle on the manager and a too long probation on the probation", async () => {
    let refusal = staffProblem(409, {
      code: "manager_cycle",
      title: "Manager cycle",
      detail: "This would make someone their own manager, through the people above them, from 2027-04-01.",
    });
    stubStaffApi({
      role: "manage",
      write: (method, path) => (method === "POST" && path.endsWith("/terms") ? refusal.clone() : undefined),
    });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "New terms" }));
    const dialog = await screen.findByRole("dialog", { name: "New terms" });
    await typeDate(within(dialog).getByRole("textbox", { name: /Takes effect/ }), "Apr 1, 2027");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save terms" }));
    expect(await within(dialog).findByText(/their own manager, through the people above them/)).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: "Manager" })).toHaveAttribute("aria-invalid", "true");

    refusal = staffProblem(400, {
      code: "probation_too_long",
      title: "Invalid terms",
      errors: { probationEndDate: ["The probation ends more than six months after the start"] },
    });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save terms" }));
    expect(
      await within(dialog).findByText("The probation ends more than six months after the start"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: /Probation ends/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("corrects a past row with the correction flag and the row's revision", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    const past = await region("Terms from Jan 1, 2025 to Feb 28, 2026");
    await userEvent.click(within(past).getByRole("button", { name: "Correct" }));
    const dialog = await screen.findByRole("dialog", { name: "Correct the terms from Jan 1, 2025" });
    // The first row takes effect on the start, whatever the form says.
    expect(within(dialog).getByRole("textbox", { name: /Takes effect/ })).toBeDisabled();
    await retype(within(dialog).getByRole("textbox", { name: "Title" }), "Engineer I");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save terms" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /terms\/301$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /terms\/301$/);
    expect(put.ifMatch).toBe("1");
    expect(put.body).toMatchObject({ correction: true, validFrom: "2025-01-01", title: "Engineer I", ftePercent: 100 });
    expect(await within(past).findByText("Engineer I")).toBeInTheDocument();
  });

  it("deletes the latest row after asking, and never offers it on the first", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    const first = await region("Terms from Jan 1, 2025 to Feb 28, 2026");
    expect(within(first).queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    const latest = await region("Terms from Mar 1, 2026");
    await userEvent.click(within(latest).getByRole("button", { name: "Delete" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Delete the terms from Mar 1, 2026?" })).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => expect(sent(fetchMock, "DELETE", /terms\/302$/)).toHaveLength(1));
    await waitFor(() =>
      expect(rowNames(screen.getByRole("region", { name: "Employment E-1-1" }))).toEqual(["Terms from Jan 1, 2025"]),
    );
    // Now the only row, it is the first: nothing to delete.
    expect(
      within(await region("Terms from Jan 1, 2025")).queryByRole("button", { name: "Delete" }),
    ).not.toBeInTheDocument();
  });
});

describe("Employments — HR ends and reopens", () => {
  it("asks for the reason before sending, then ends and offers Reopen", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "End employment" }));
    const dialog = await screen.findByRole("dialog", { name: "End the employment E-1-1" });
    await userEvent.click(within(dialog).getByRole("button", { name: "End employment" }));
    expect(await within(dialog).findByText("Choose the reason")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/end$/)).toHaveLength(0);

    await choose(dialog, "End reason", "The employee resigned");
    await userEvent.click(within(dialog).getByRole("button", { name: "End employment" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /employments\/201\/end$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /employments\/201\/end$/)[0].body).toEqual({
      endDate: "2026-10-11",
      endReason: "employee_resigned",
    });
    const card = await adaEmployment();
    expect(await within(card).findByText("The employee resigned")).toBeInTheDocument();

    await userEvent.click(within(card).getByRole("button", { name: "Reopen" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Reopen the employment?" })).getByRole("button", {
        name: "Reopen",
      }),
    );
    await waitFor(() => expect(sent(fetchMock, "POST", /employments\/201\/reopen$/)).toHaveLength(1));
    await waitFor(() => expect(within(card).queryByText("The employee resigned")).not.toBeInTheDocument());
    expect(within(card).getByRole("button", { name: "End employment" })).toBeInTheDocument();
  });

  it("puts terms after the end on the end date", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "End employment" }));
    const dialog = await screen.findByRole("dialog", { name: "End the employment E-1-1" });
    await typeDate(within(dialog).getByRole("textbox", { name: /Last day worked/ }), "Jan 31, 2026");
    await choose(dialog, "End reason", "The employer terminated");
    await userEvent.click(within(dialog).getByRole("button", { name: "End employment" }));

    expect(await within(dialog).findByText("A terms row takes effect after this end.")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: /Last day worked/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("edits the employment with its revision, the number shown and never sent changed", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    await userEvent.click(within(await adaEmployment()).getByRole("button", { name: "Edit employment" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit employment E-1-1" });
    expect(within(dialog).getByText(/never changes/)).toBeInTheDocument();
    await choose(dialog, "Business unit", "Bergen office");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /employments\/201$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /employments\/201$/);
    expect(put.ifMatch).toBe("2");
    expect(put.body).toEqual({
      startDate: "2025-01-01",
      businessUnitId: 12,
      relationshipType: "ordinary",
      contractSignedAt: "2024-12-15",
    });
  });
});

describe("Employments — the contract check", () => {
  it("lists the seventeen items with present, missing and not applicable, and the signed flag", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=employments");

    const check = await region("Contract check");
    const items = await within(check).findAllByRole("listitem");
    expect(items).toHaveLength(17);
    const item = (name: RegExp) => items.find((one) => name.test(one.textContent ?? "")) as HTMLElement;
    expect(item(/^b\b.*Workplace/)).toHaveTextContent("Present");
    expect(item(/^e\b.*Duration/)).toHaveTextContent("Not applicable");
    expect(item(/^j\b.*Working time/)).toHaveTextContent("Missing");
    expect(item(/^q\b.*Social security/)).toHaveTextContent("Present");
    expect(within(check).getByText("Contract signed on Dec 15, 2024")).toBeInTheDocument();
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, staffProblem, stubStaffApi } from "../test/server";

const sent = (fetchMock: StaffStub, method: string, pattern: RegExp) => {
  const calls = fetchMock.actualCalls.filter(
    ([url, init]) => (init?.method ?? "GET") === method && pattern.test(String(url)),
  );
  return calls.map(([url, init]) => ({
    url: String(url),
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  }));
};

const open = async () => {
  renderRoute("/staff/people");
  await userEvent.click(await screen.findByRole("button", { name: "New person" }));
  return screen.findByRole("dialog", { name: "New person" });
};

const choose = async (dialog: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(dialog).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

/** Fills in and saves step 1 with the names alone. */
const personStep = async (dialog: HTMLElement) => {
  await userEvent.type(within(dialog).getByRole("textbox", { name: "Given name" }), "Margaret");
  await userEvent.type(within(dialog).getByRole("textbox", { name: "Family name" }), "Hamilton");
  await userEvent.click(within(dialog).getByRole("button", { name: "Save and continue" }));
  await within(dialog).findByText(/Margaret Hamilton \(E-8\) is saved/);
};

describe("New person — step 1, the person", () => {
  it("refuses a person without names, without asking the server", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();

    await userEvent.click(within(dialog).getByRole("button", { name: "Save and continue" }));

    expect(await within(dialog).findByText("Give the given name")).toBeInTheDocument();
    expect(within(dialog).getByText("Give the family name")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/staff\/people$/)).toHaveLength(0);
  });

  it("generates the employee number by default, saying which comes next", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();

    expect(await within(dialog).findByText(/E-8 comes next/)).toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Employee number" })).not.toBeInTheDocument();
    await personStep(dialog);

    const [created] = sent(fetchMock, "POST", /\/staff\/people$/);
    expect(created.body).toEqual({ givenName: "Margaret", familyName: "Hamilton", preferredLanguage: "nb" });
  });

  it("sends the number typed when HR assigns their own", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();

    await userEvent.click(within(dialog).getByRole("switch", { name: "Assign my own employee number" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Given name" }), "Margaret");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Family name" }), "Hamilton");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save and continue" }));
    expect(await within(dialog).findByText("Give the employee number, or let it be generated")).toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Employee number" }), "MH-1969");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save and continue" }));
    await within(dialog).findByText(/Margaret Hamilton \(MH-1969\) is saved/);

    expect(sent(fetchMock, "POST", /\/staff\/people$/)[0].body).toMatchObject({ employeeNumber: "MH-1969" });
  });

  it("puts a work email another person has on the work email field", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/people"
          ? staffProblem(400, {
              code: "work_email_taken",
              title: "Invalid person",
              errors: { workEmail: ["Another person has the work email ada@fjord.example"] },
            })
          : undefined,
    });
    const dialog = await open();

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Given name" }), "Ada");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Family name" }), "Byron");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Work email" }), "ada@fjord.example");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save and continue" }));

    expect(await within(dialog).findByText("Another person has the work email ada@fjord.example")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Work email" })).toHaveAttribute("aria-invalid", "true");
  });

  it("keeps the private details optional and folded away until asked for", async () => {
    stubStaffApi();
    const dialog = await open();

    expect(within(dialog).queryByRole("textbox", { name: "Private email" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Private details (optional)" }));
    expect(within(dialog).getByRole("textbox", { name: "Private email" })).toBeInTheDocument();
  });
});

describe("New person — step 2, the first employment", () => {
  it("refuses terms without the type and the percentage, explaining the presumption", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    expect(within(dialog).getByText(/the law presumes what the employee claims/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    expect(await within(dialog).findByText(/State the employment type/)).toBeInTheDocument();
    expect(within(dialog).getByText(/State the percentage of a full position/)).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(0);
  });

  it("creates the employment with its first terms as the form shows them, and opens the person", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    expect(within(dialog).getByText(/E-8-1/)).toBeInTheDocument();
    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "80");
    await choose(dialog, "Department", "Engineering (ENG)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Title" }), "Flight software lead");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(1));
    const [employment] = sent(fetchMock, "POST", /\/employments$/);
    expect(employment.url).toBe("/api/v1/staff/people/900/employments");
    expect(employment.body).toEqual({
      businessUnitId: 11,
      startDate: "2026-10-11",
      terms: {
        employmentType: "fast",
        appointmentForm: "fast",
        ftePercent: 80,
        workingHoursScheme: "not_shift",
        fullTimeHoursPerWeek: 37.5,
        departmentId: 21,
        title: "Flight software lead",
      },
    });
    expect(await screen.findByTestId("person-page")).toHaveTextContent("900");
  });

  it("asks for the ground of a temporary employment and offers only the forms that go with it", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    await choose(dialog, "Employment type", "Temporary (midlertidig)");
    expect(within(dialog).getByRole("combobox", { name: "Appointment form" })).toHaveValue("Temporary");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Appointment form" }));
    expect(screen.queryByRole("option", { name: "Permanent" })).not.toBeInTheDocument();
    expect(await screen.findByRole("option", { name: "Temporary, on-call substitute" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));
    expect(await within(dialog).findByText("A temporary employment states its ground")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(0);
  });

  it("follows the scheme's hours and shows a mismatch before anything is sent", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await choose(dialog, "Working-hours scheme", /skift365/);
    const hours = within(dialog).getByRole("textbox", { name: "Hours of a full position per week" });
    expect(hours).toHaveValue("36.5");

    await userEvent.clear(hours);
    await userEvent.type(hours, "37.5");
    expect(await within(dialog).findByText("This scheme requires exactly 36.5 hours a week")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));
    expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(0);

    await choose(dialog, "Working-hours scheme", /offshore336/);
    expect(hours).toHaveValue("37.5");
    expect(await within(dialog).findByText("This scheme allows 28 to 33.6 hours a week")).toBeInTheDocument();
  });

  it("sends the employment number typed when HR assigns their own", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Assign my own employment number" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Employment number" }), "AF-2026-1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/employments$/)[0].body).toMatchObject({ employmentNumber: "AF-2026-1" });
  });

  it("puts the server's field error on the field it names, nested ones included", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path.endsWith("/employments")
          ? staffProblem(400, {
              code: "full_time_hours_mismatch",
              title: "Invalid employment",
              errors: { "terms.fullTimeHoursPerWeek": ["The hours of a full position do not fit the scheme"] },
            })
          : undefined,
    });
    const dialog = await open();
    await personStep(dialog);

    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    expect(await within(dialog).findByText("The hours of a full position do not fit the scheme")).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Hours of a full position per week" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it("unfolds More terms when the server refuses a field folded under it", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path.endsWith("/employments")
          ? staffProblem(400, {
              title: "Invalid employment",
              errors: { contractSignedAt: ["The contract cannot be signed after the start's year ends"] },
            })
          : undefined,
    });
    const dialog = await open();
    await personStep(dialog);

    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    expect(
      await within(dialog).findByText("The contract cannot be signed after the start's year ends"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Contract signed" })).toHaveAttribute("aria-invalid", "true");
  });

  it("folds the rest of the terms under More terms", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    expect(within(dialog).queryByRole("textbox", { name: "Working time" })).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "More terms" }));
    await choose(dialog, "Employment type", "Permanent (fast)");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Working time" }), "08:00–16:00 weekdays");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/employments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/employments$/)[0].body.terms).toMatchObject({
      workingTimeText: "08:00–16:00 weekdays",
    });
  });

  it("lets the employment wait: the person stays, findable, and their page opens", async () => {
    const fetchMock = stubStaffApi();
    const dialog = await open();
    await personStep(dialog);

    await userEvent.click(within(dialog).getByRole("button", { name: "Add it later" }));
    expect(await screen.findByTestId("person-page")).toHaveTextContent("900");
    expect(fetchMock.state.people.find((one) => one.id === 900)?.status.state).toBe("none");
  });
});

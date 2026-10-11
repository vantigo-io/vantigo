import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { settings } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, staffProblem, stubStaffApi } from "../test/server";

/** The first business request with this method whose path ends with `endsWith`: its URL, If-Match and body. */
const write = (fetchMock: StaffStub, method: string, endsWith: string) => {
  const [url, init] =
    fetchMock.actualCalls.find(
      ([candidate, request]) => (request?.method ?? "GET") === method && String(candidate).endsWith(endsWith),
    ) ?? [];
  return {
    url: url === undefined ? undefined : String(url),
    ifMatch: init?.headers ? new Headers(init.headers).get("If-Match") : null,
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  };
};

const tableRow = async (table: string, name: string) => {
  const found = await screen.findByRole("table", { name: table });
  // The first cell that says it: a name may also be another row's parent, or a location's city.
  return (await within(found).findAllByText(name))[0].closest("tr") as HTMLElement;
};

/** Picks an option from a Mantine select. */
const choose = async (scope: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(scope).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

describe("SettingsPage — the settings", () => {
  it("shows the next generated number and its length, and saves with the revision it read", async () => {
    const fetchMock = stubStaffApi({ settings: settings({ employeeNumberPrefix: "E-", nextEmployeeNumber: 8 }) });
    renderRoute("/staff/settings");

    expect(await screen.findByText(/The next generated number is E-8/)).toHaveTextContent("3 of at most 16 characters");
    const insurer = screen.getByRole("textbox", { name: "Occupational injury insurer" });
    await userEvent.type(insurer, "If Skadeforsikring");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    await waitFor(() => expect(write(fetchMock, "PUT", "/staff/settings").url).toBeDefined());
    const sent = write(fetchMock, "PUT", "/staff/settings");
    expect(sent.ifMatch).toBe("3");
    expect(sent.body).toEqual({
      timeZone: "Europe/Oslo",
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      injuryInsurer: "If Skadeforsikring",
    });
  });

  it("refuses a prefix and next number that would make a number longer than 16 characters, without asking", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const prefix = await screen.findByRole("textbox", { name: "Employee number prefix" });
    await userEvent.clear(prefix);
    await userEvent.type(prefix, "FJORD-");
    const next = screen.getByRole("textbox", { name: "Next employee number" });
    await userEvent.clear(next);
    await userEvent.type(next, "12345678901");

    expect(await screen.findByText(/The next generated number is FJORD-12345678901/)).toHaveTextContent(
      "17 of at most 16 characters",
    );
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect((await screen.findAllByText(/longer than an employee number may be/)).length).toBeGreaterThan(0);
    expect(write(fetchMock, "PUT", "/staff/settings").url).toBeUndefined();
  });

  it("puts the server's refusal on the field it names", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/settings"
          ? staffProblem(400, {
              title: "Invalid Staff settings",
              errors: { timeZone: ["Mars/Olympus is not a time zone this server knows"] },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await screen.findByRole("textbox", { name: "Employee number prefix" });
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("Mars/Olympus is not a time zone this server knows")).toBeInTheDocument();
  });
});

describe("SettingsPage — the structure", () => {
  it("marks the default legal entity, and moves the default with the row's own revision", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const first = await tableRow("Legal entities", "Fjord Systems AS");
    expect(first).toHaveTextContent("Default");
    expect(within(first).queryByRole("button", { name: /Make .* the default/ })).not.toBeInTheDocument();
    const second = await tableRow("Legal entities", "Fjord Holding AS");
    expect(second).not.toHaveTextContent("Default");

    await userEvent.click(within(second).getByRole("button", { name: "Make Fjord Holding AS the default" }));

    await waitFor(() => expect(write(fetchMock, "PUT", "/legal-entities/2").url).toBeDefined());
    const sent = write(fetchMock, "PUT", "/legal-entities/2");
    expect(sent.ifMatch).toBe("4");
    expect(sent.body).toEqual({ name: "Fjord Holding AS", isDefault: true });

    await waitFor(async () =>
      expect(await tableRow("Legal entities", "Fjord Holding AS")).toHaveTextContent("Default"),
    );
    expect(await tableRow("Legal entities", "Fjord Systems AS")).not.toHaveTextContent("Default");
  });

  it("says the server's own sentence when archiving the default is refused", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");

    const unit = await tableRow("Business units", "Oslo office");
    await userEvent.click(within(unit).getByRole("button", { name: "Archive Oslo office" }));
    const confirm = await screen.findByRole("dialog", { name: "Archive Oslo office?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Archive" }));

    expect(
      await screen.findByText(
        "This is the default business unit, and there is always exactly one. Make another business unit the default first.",
      ),
    ).toBeInTheDocument();
    expect(await tableRow("Business units", "Oslo office")).not.toHaveTextContent("Archived");
  });

  it("archives a row that is not the default, which then stays listed as archived without actions", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const bergen = await tableRow("Locations", "Bergen");
    await userEvent.click(within(bergen).getByRole("button", { name: "Archive Bergen" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Archive" }));

    await waitFor(() => expect(write(fetchMock, "POST", "/locations/32/archive").url).toBeDefined());
    await waitFor(async () => expect(await tableRow("Locations", "Bergen")).toHaveTextContent("Archived"));
    expect(within(await tableRow("Locations", "Bergen")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("lists each business unit under its legal entity, the archived one marked", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");

    expect(await tableRow("Business units", "Bergen office")).toHaveTextContent("Fjord Holding AS");
    expect(await tableRow("Business units", "Oslo office")).toHaveTextContent("Fjord Systems AS");
    expect(await tableRow("Business units", "Old Drammen office")).toHaveTextContent("Archived");
  });

  it("names a department's manager and parent, and picks a new department's manager from the people", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    expect(await tableRow("Departments", "Engineering")).toHaveTextContent("Ada Lovelace");
    expect(await tableRow("Departments", "Operations")).toHaveTextContent("Engineering");

    await userEvent.click(screen.getByRole("button", { name: "Add department" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a department" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Code" }), "SAL");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Sales");
    const manager = within(dialog).getByRole("combobox", { name: "Manager" });
    await userEvent.type(manager, "grace");
    await userEvent.click(await screen.findByRole("option", { name: "Grace Hopper (E-2)" }));
    await choose(dialog, "Parent department", "Engineering (ENG)");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(write(fetchMock, "POST", "/staff/departments").url).toBeDefined());
    expect(write(fetchMock, "POST", "/staff/departments").body).toEqual({
      code: "SAL",
      name: "Sales",
      managerPersonId: 102,
      parentId: 21,
    });
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/staff/people?q=grace"))).toBe(true);
  });

  it("adds a business unit under the legal entity picked, with no default flag unless asked", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Add business unit" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a business unit" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Trondheim office");
    await choose(dialog, "Legal entity", "Fjord Holding AS");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(write(fetchMock, "POST", "/staff/business-units").url).toBeDefined());
    expect(write(fetchMock, "POST", "/staff/business-units").body).toEqual({
      name: "Trondheim office",
      legalEntityId: 2,
    });
  });

  it("puts an organisation number the server refuses on its field", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/legal-entities"
          ? staffProblem(400, {
              code: "org_number_invalid",
              title: "Invalid legal entity",
              errors: { orgNumber: ["An organisation number is nine digits with a valid check digit"] },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Add legal entity" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a legal entity" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Fjord Invest AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Organisation number" }), "123456789");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(
      await within(dialog).findByText("An organisation number is nine digits with a valid check digit"),
    ).toBeInTheDocument();
  });
});

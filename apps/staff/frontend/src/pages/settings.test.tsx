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
    // The account settings (delivery C) are sent back as read; "never" is null.
    const fetchMock = stubStaffApi({
      settings: settings({
        employeeNumberPrefix: "E-",
        nextEmployeeNumber: 8,
        createBeforeDays: 0,
        eraseSickCaseDates: true,
      }),
    });
    renderRoute("/staff/settings");

    expect(await screen.findByText(/The next generated number is E-8/)).toHaveTextContent("3 of at most 16 characters");
    const insurer = screen.getByRole("textbox", { name: "Occupational injury insurer" });
    await userEvent.type(insurer, "If Skadeforsikring");
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    await waitFor(() => expect(write(fetchMock, "PUT", "/staff/settings").url).toBeDefined());
    const sent = write(fetchMock, "PUT", "/staff/settings");
    expect(sent.ifMatch).toBe("3");
    expect(sent.body).toEqual({
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      injuryInsurer: "If Skadeforsikring",
      createBeforeDays: 0,
      deleteExternalAfterDays: null,
      erasePregnancyHealthTypes: false,
      eraseSickCaseDates: true,
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
              errors: { pensionProvider: ["A pension provider cannot be longer than 200 characters"] },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await screen.findByRole("textbox", { name: "Employee number prefix" });
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("A pension provider cannot be longer than 200 characters")).toBeInTheDocument();
  });
});

/** The module's 409 for a stale If-Match, with the English sentence the page must not show. */
const revisionConflict = () =>
  staffProblem(409, {
    code: "revision_conflict",
    title: "Revision conflict",
    detail: "The settings has revision 4; the supplied revision was 3.",
  });

describe("SettingsPage — somebody else saved first", () => {
  it("reloads the settings and says so in the reader's words", async () => {
    const fetchMock = stubStaffApi({
      write: (method, path) => (method === "PUT" && path === "/api/v1/staff/settings" ? revisionConflict() : undefined),
    });
    renderRoute("/staff/settings");

    const insurer = await screen.findByRole("textbox", { name: "Occupational injury insurer" });
    const reads = fetchMock.actualCalls.filter(
      ([url, init]) => String(url).endsWith("/staff/settings") && !init?.method,
    ).length;
    await userEvent.type(insurer, "Gjensidige");
    // Meanwhile the row moved on, as somebody else's save would leave it.
    fetchMock.state.settings = { ...fetchMock.state.settings, injuryInsurer: "Tryg", revision: 4 };
    await userEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Occupational injury insurer" })).toHaveValue("Tryg"),
    );
    expect(
      fetchMock.actualCalls.filter(([url, init]) => String(url).endsWith("/staff/settings") && !init?.method).length,
    ).toBeGreaterThan(reads);
  });

  it("closes a structure form whose row moved on, and reloads the table", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/locations/31" ? revisionConflict() : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Locations", "Oslo HQ")).getByRole("button", { name: "Change Oslo HQ" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Change the location" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Change the location" })).not.toBeInTheDocument());
  });

  it("says the same when the default moved under a stale row", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path === "/api/v1/staff/legal-entities/2" ? revisionConflict() : undefined,
    });
    renderRoute("/staff/settings");

    const second = await tableRow("Legal entities", "Fjord Holding AS");
    await userEvent.click(within(second).getByRole("button", { name: "Make Fjord Holding AS the default" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
  });
});

describe("SettingsPage — HR without the directory", () => {
  it("reads and changes the structure with staff:manage alone", async () => {
    const fetchMock = stubStaffApi({ role: "manageOnly" });
    renderRoute("/staff/settings");

    expect(await tableRow("Legal entities", "Fjord Systems AS")).toHaveTextContent("Default");
    expect(await tableRow("Business units", "Bergen office")).toHaveTextContent("Fjord Holding AS");
    expect(await tableRow("Departments", "Operations")).toHaveTextContent("Engineering");
    expect(await tableRow("Locations", "Bergen")).toBeInTheDocument();
    await userEvent.click(
      within(await tableRow("Locations", "Bergen")).getByRole("button", { name: "Archive Bergen" }),
    );
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(write(fetchMock, "POST", "/locations/32/archive").url).toBeDefined());
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
    // The picker searches the server as one types: wait on its answer, which
    // a slow runner gives late, rather than on the default second.
    await userEvent.click(await screen.findByRole("option", { name: "Grace Hopper (E-2)" }, { timeout: 10_000 }));
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

  it("names a manager it cannot read by id rather than waiting for ever", async () => {
    stubStaffApi({ departments: [{ id: 23, code: "LEG", name: "Legal", managerPersonId: 999, revision: 1 }] });
    renderRoute("/staff/settings");

    expect(await tableRow("Departments", "Legal")).toHaveTextContent("Person 999");
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

describe("SettingsPage — the document categories", () => {
  it("lists the categories with their class, the seeds fixed and the archived one marked", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");

    const health = await tableRow("Document categories", "health");
    expect(health).toHaveTextContent("Helse");
    expect(within(health).getByText("E · Health")).toBeInTheDocument();
    expect(health).toHaveTextContent("Yes");
    expect(health).toHaveTextContent("Seeded");
    expect(within(health).queryByRole("button")).not.toBeInTheDocument();
    const training = await tableRow("Document categories", "training");
    expect(within(training).getByText("D · Personnel")).toBeInTheDocument();
    expect(within(training).getByRole("button", { name: "Change Training record" })).toBeInTheDocument();
    expect(within(training).getByRole("button", { name: "Archive Training record" })).toBeInTheDocument();
    const old = await tableRow("Document categories", "old-forms");
    expect(old).toHaveTextContent("Archived");
    expect(within(old).getByRole("button", { name: "Restore Old forms" })).toBeInTheDocument();
  });

  it("adds a company category, its class fixed and restricted following from it", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    await userEvent.click(await screen.findByRole("button", { name: "Add category" }));
    const dialog = await screen.findByRole("dialog", { name: "Add document category" });
    expect(within(dialog).getByText(/cannot be changed once the category is created/)).toBeInTheDocument();
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Key/ }), "occupational-health");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /English name/ }), "Occupational health");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Norwegian name/ }), "Bedriftshelse");
    await choose(dialog, "Class", /^E · Health/);
    expect(within(dialog).getByText(/Restricted: its documents are health documents/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(write(fetchMock, "POST", "/document-categories").body).toBeDefined());
    expect(write(fetchMock, "POST", "/document-categories").body).toEqual({
      key: "occupational-health",
      names: { en: "Occupational health", nb: "Bedriftshelse" },
      class: "E",
    });
    expect(await tableRow("Document categories", "occupational-health")).toHaveTextContent("Yes");
  });

  it("changes a company category's names only, with the revision it read", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Document categories", "training")).getByRole("button", {
        name: "Change Training record",
      }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Change document category" });
    expect(within(dialog).queryByRole("textbox", { name: /Key/ })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("combobox", { name: "Class" })).not.toBeInTheDocument();
    const nb = within(dialog).getByRole("textbox", { name: /Norwegian name/ });
    await userEvent.clear(nb);
    await userEvent.type(nb, "Kursbevis");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(write(fetchMock, "PUT", "/document-categories/7").body).toBeDefined());
    const put = write(fetchMock, "PUT", "/document-categories/7");
    expect(put.ifMatch).toBe("2");
    expect(put.body).toEqual({ names: { en: "Training record", nb: "Kursbevis" }, archived: false });
  });

  it("says category_in_use in words when documents are filed under it", async () => {
    stubStaffApi({
      documents: [
        {
          id: 590,
          personId: 101,
          category: {
            id: 7,
            key: "training",
            names: { en: "Training record", nb: "Opplæringsbevis" },
            class: "D",
            restricted: false,
          },
          fileName: "course.pdf",
          contentType: "application/pdf",
          sizeBytes: 1000,
          uploadedAt: "2026-01-01T10:00:00Z",
          revision: 1,
        },
      ],
    });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Document categories", "training")).getByRole("button", {
        name: "Archive Training record",
      }),
    );

    expect(
      await screen.findByText("Documents are filed under Training record. Move them to another category first."),
    ).toBeInTheDocument();
    expect(await tableRow("Document categories", "training")).not.toHaveTextContent("Archived");
  });

  it("says seed_immutable in words", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/document-categories/7")
          ? staffProblem(409, { code: "seed_immutable", title: "A seeded category", detail: "seed" })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Document categories", "training")).getByRole("button", {
        name: "Archive Training record",
      }),
    );

    expect(await screen.findByText("The six seeded categories never change.")).toBeInTheDocument();
  });

  it("archives a category nothing is filed under, and restores it", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await tableRow("Document categories", "training")).getByRole("button", {
        name: "Archive Training record",
      }),
    );
    await waitFor(async () => expect(await tableRow("Document categories", "training")).toHaveTextContent("Archived"));
    expect(write(fetchMock, "PUT", "/document-categories/7").body).toEqual({
      names: { en: "Training record", nb: "Opplæringsbevis" },
      archived: true,
    });
    await userEvent.click(
      within(await tableRow("Document categories", "training")).getByRole("button", {
        name: "Restore Training record",
      }),
    );
    await waitFor(async () =>
      expect(await tableRow("Document categories", "training")).not.toHaveTextContent("Archived"),
    );
  });

  it("sends an archive once, however often it is pressed while on its way", async () => {
    const fetchMock = stubStaffApi({
      write: (method, path) =>
        method === "PUT" && path.endsWith("/document-categories/7")
          ? (new Promise(() => {}) as unknown as Response)
          : undefined,
    });
    renderRoute("/staff/settings");

    const archive = within(await tableRow("Document categories", "training")).getByRole("button", {
      name: "Archive Training record",
    });
    await userEvent.click(archive);
    await userEvent.click(archive);

    expect(archive).toBeDisabled();
    expect(fetchMock.actualCalls.filter(([, init]) => init?.method === "PUT")).toHaveLength(1);
  });
});

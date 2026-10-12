import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { jsonResponse } from "../test/api";
import { importTally, settings } from "../test/fixtures";
import { awaitDownload, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, staffProblem, stubStaffApi } from "../test/server";

/**
 * The anchor the page clicks to hand a file over, and the blobs it made
 * object URLs of — the failed-rows file is built in the browser, so its bytes
 * are read back from here.
 */
const catchDownloads = () => {
  const { createObjectURL, revokeObjectURL } = URL;
  const blobs: Blob[] = [];
  URL.createObjectURL = vi.fn((blob: Blob) => {
    blobs.push(blob);
    return "blob:staff";
  });
  URL.revokeObjectURL = vi.fn();
  onTestFinished(() => {
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
  });
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  const names = () => click.mock.contexts.map((anchor) => (anchor as HTMLAnchorElement).download);
  return { blobs, names, click };
};

/** The imports the fake received: the dry-run flag of the URL and the file part. */
const imports = (fetchMock: StaffStub) =>
  fetchMock.actualCalls
    .filter(([url, init]) => init?.method === "POST" && String(url).includes("/api/v1/staff/people/import"))
    .map(([url, init]) => ({
      dryRun: new URL(String(url), "http://localhost").searchParams.get("dryRun"),
      file: (init?.body as FormData | undefined)?.get("file") as File,
    }));

// An all-blank record before the failing one takes no number, so row 2 is
// `E-9;…` only if the browser numbers rows exactly as the server does.
const FILE_TEXT =
  "\ufeffemployeeNumber;givenName;familyName;workEmail\r\nE-8;Ny;Person;ny@fjord.no\r\n;;;\r\nE-9;Gammel;Person;nope\r\nE-1;Ada;Lovelace;ada@fjord.no\r\n";
const EMAIL_ERROR = "A work email must look like name@example.com, but was 'nope'";

const openImport = async () => {
  await userEvent.click(
    within(await region("Import and export people")).getByRole("button", { name: "Import people" }),
  );
  return screen.findByRole("dialog", { name: "Import people" });
};

const chooseFile = async (dialog: HTMLElement, file = new File([FILE_TEXT], "people.csv", { type: "text/csv" })) =>
  userEvent.upload(within(dialog).getByLabelText("Choose CSV file"), file);

/** The figure shown under `label`: the counts are labelled numbers, not a sentence. */
const figure = (scope: HTMLElement, label: string) => within(scope).getByText(label).nextElementSibling;

describe("Settings — the data cards are HR's", () => {
  it("shows none of them to a colleague holding staff:view", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/settings");

    await screen.findByRole("table", { name: "Legal entities" });
    for (const name of ["Import and export people", "Payroll export", "Retention", "Thresholds"])
      expect(screen.queryByRole("region", { name })).not.toBeInTheDocument();
  });

  it("shows all four to staff:manage without the directory", async () => {
    stubStaffApi({ role: "manageOnly" });
    renderRoute("/staff/settings");

    for (const name of ["Import and export people", "Payroll export", "Retention", "Thresholds"])
      expect(await region(name)).toBeInTheDocument();
  });
});

describe("Settings — import people", () => {
  it("checks the file, imports it, and hands back the failed rows behind an error column", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const dialog = await openImport();
    expect(within(dialog).getByText(/ignores the error column/)).toBeInTheDocument();
    await chooseFile(dialog);
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));

    expect(await within(dialog).findByText("Would be created")).toBeInTheDocument();
    expect(figure(dialog, "Rows")).toHaveTextContent("3");
    expect(figure(dialog, "Would be created")).toHaveTextContent("1");
    expect(figure(dialog, "Would be updated")).toHaveTextContent("1");
    expect(figure(dialog, "Have errors")).toHaveTextContent("1");
    const problems = within(dialog).getByRole("table", { name: "Problems in the file, by row" });
    const errorRow = within(problems).getByText(EMAIL_ERROR).closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("2")).toBeInTheDocument();
    expect(within(errorRow).getByText("workEmail")).toBeInTheDocument();
    expect(imports(fetchMock)).toHaveLength(1);
    expect(imports(fetchMock)[0].dryRun).toBe("true");
    expect(imports(fetchMock)[0].file.name).toBe("people.csv");

    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));
    expect(await within(dialog).findByText("Created")).toBeInTheDocument();
    expect(figure(dialog, "Created")).toHaveTextContent("1");
    expect(figure(dialog, "Updated")).toHaveTextContent("1");
    expect(figure(dialog, "Failed")).toHaveTextContent("1");
    expect(imports(fetchMock).map((one) => one.dryRun)).toEqual(["true", "false"]);
    // The real run sends the bytes the check read.
    expect(await imports(fetchMock)[1].file.text()).toBe(await imports(fetchMock)[0].file.text());

    await userEvent.click(within(dialog).getByRole("button", { name: "Download failed rows" }));
    await awaitDownload(downloads.click);
    expect(downloads.names()).toEqual(["people-failed-rows.csv"]);
    // Blob.text() strips a leading BOM; the file keeps it, so the bytes are decoded with it.
    const text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(await downloads.blobs[0].arrayBuffer());
    expect(text).toBe(
      `\ufefferror;employeeNumber;givenName;familyName;workEmail\r\nworkEmail: ${EMAIL_ERROR};E-9;Gammel;Person;nope\r\n`,
    );
  });

  it("refuses a file larger than 5 MiB before sending it", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog, new File([new Uint8Array(5 * 1024 * 1024 + 1)], "huge.csv", { type: "text/csv" }));

    expect(await within(dialog).findByText(/huge\.csv is larger than 5 MiB/)).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Check" })).toBeDisabled();
    expect(imports(fetchMock)).toHaveLength(0);
  });

  it("says in words why a file with the sealed columns is refused whole, with the server's own sentence", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/people/import"
          ? staffProblem(400, {
              code: "import_sealed_columns",
              title: "Invalid import file",
              errors: {
                file: [
                  "The columns nationalIdKind, nationalId need the staff:personal-data permission, which you do not have: remove them from the file, or ask for the permission",
                ],
              },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    // Said before anything is sent, to someone without staff:personal-data.
    expect(within(dialog).getByText(/need the staff:personal-data permission as well/)).toBeInTheDocument();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));

    const banner = await within(dialog).findByRole("alert");
    expect(banner).toHaveTextContent("The file was refused");
    expect(banner).toHaveTextContent(
      "The file has sealed columns — the national identity number or the bank account — and importing them needs staff:personal-data as well.",
    );
    expect(banner).toHaveTextContent("The columns nationalIdKind, nationalId need the staff:personal-data permission");
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("says a comma-separated file is refused in words", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/people/import"
          ? staffProblem(400, {
              code: "import_not_semicolon",
              title: "Invalid import file",
              errors: { file: ["The file is comma-separated; the import reads semicolon-separated files"] },
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The file is separated by commas. Save it from the spreadsheet as CSV with semicolons",
    );
  });

  it("explains a real run cut short: what was imported stays, and Import waits for a new check", async () => {
    let posts = 0;
    stubStaffApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/api/v1/staff/people/import") return undefined;
        posts += 1;
        // The check answers; the real run's connection drops.
        return posts === 1 ? undefined : (Promise.reject(new TypeError("Failed to fetch")) as unknown as Response);
      },
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));
    await within(dialog).findByText("Would be created");
    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The rows that were imported before it stopped stay imported; check the file and run it again.",
    );
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("explains a real run the server failed part-way as cut short too", async () => {
    let posts = 0;
    stubStaffApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/api/v1/staff/people/import") return undefined;
        posts += 1;
        return posts === 1 ? undefined : staffProblem(500, { title: "Internal Server Error" });
      },
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));
    await within(dialog).findByText("Would be created");
    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The rows that were imported before it stopped stay imported; check the file and run it again.",
    );
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it.each([
    ["import_too_many_rows", "The file has more rows than one import takes. Import it in slices."],
    ["import_too_large", "The file is larger than 5 MiB, the most one import takes. Import it in slices."],
  ])("says %s in words", async (code, words) => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/people/import"
          ? staffProblem(400, { code, title: "Invalid import file", errors: { file: ["The server's sentence"] } })
          : undefined,
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));

    const banner = await within(dialog).findByRole("alert");
    expect(banner).toHaveTextContent(words);
    expect(banner).toHaveTextContent("The server's sentence");
  });

  it("holds the file still while a check runs, so Import sends the file the check read", async () => {
    let answer: (response: Response) => void = () => {};
    // The first check answers only when the test says so.
    let posts = 0;
    const held = stubStaffApi({
      write: (method, path) => {
        if (method !== "POST" || path !== "/api/v1/staff/people/import") return undefined;
        posts += 1;
        if (posts > 1) return undefined;
        return new Promise<Response>((resolve) => {
          answer = resolve;
        }) as unknown as Response;
      },
    });
    renderRoute("/staff/settings");

    const dialog = await openImport();
    await chooseFile(dialog);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check" }));
    // Another file picked while the check of the first is on its way.
    fireEvent.change(within(dialog).getByLabelText("Choose CSV file"), {
      target: { files: [new File(["employeeNumber\r\nE-5\r\n"], "other.csv", { type: "text/csv" })] },
    });
    answer(jsonResponse(200, { ...importTally(), dryRun: true }));

    await within(dialog).findByText("Would be created");
    expect(within(dialog).getByText("Chosen: people.csv")).toBeInTheDocument();
    expect(within(dialog).queryByText("Chosen: other.csv")).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));
    await within(dialog).findByText("Created");
    expect(imports(held).map((one) => one.file.name)).toEqual(["people.csv", "people.csv"]);
  });
});

describe("Settings — export people", () => {
  it("downloads the register and the template under the names the server gives them", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Import and export people");
    expect(within(card).getByText(/never the national identity number or the bank account/)).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Export people" }));
    await awaitDownload(downloads.click);
    expect(downloads.names()).toEqual(["staff-people-2026-10-11.csv"]);
    await userEvent.click(within(card).getByRole("button", { name: "Download template" }));
    await waitFor(
      () => expect(downloads.names()).toEqual(["staff-people-2026-10-11.csv", "staff-people-import-template.csv"]),
      { timeout: 5000 },
    );
    expect(sent(fetchMock, "GET", /\/people\/export\.csv$/)).toHaveLength(1);
  });

  it("says export_too_many in words", async () => {
    catchDownloads();
    stubStaffApi({
      read: (path) =>
        path === "/api/v1/staff/people/export.csv"
          ? staffProblem(400, {
              code: "export_too_many",
              title: "Too many people to export",
              detail: "The register holds more than 5000 people, which is more than one file holds.",
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await region("Import and export people")).getByRole("button", { name: "Export people" }),
    );
    expect(
      await screen.findByText(
        "The register holds more people than one file may hold. Ask the administrator to raise STAFF_IMPORT_MAX_ROWS.",
      ),
    ).toBeInTheDocument();
  });
});

describe("Settings — the payroll export", () => {
  it("exports the previous month by default, as YYYY-MM, under the server's file name", async () => {
    const downloads = catchDownloads();
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Payroll export");
    expect(within(card).getByText(/no national identity numbers and no bank accounts/)).toBeInTheDocument();
    expect(within(card).getByText(/not verified against Skatteetaten's code list/)).toBeInTheDocument();
    expect(within(card).getByRole("button", { name: /Month/ })).toHaveTextContent("September 2026");
    await userEvent.click(within(card).getByRole("button", { name: "Export for payroll" }));

    await awaitDownload(downloads.click);
    expect(downloads.names()).toEqual(["staff-payroll-2026-09.csv"]);
    expect(sent(fetchMock, "GET", /\/payroll\/export/).map((one) => one.url)).toEqual([
      "/api/v1/staff/payroll/export?month=2026-09&format=csv",
    ]);
  });

  it("exports another month picked", async () => {
    catchDownloads();
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Payroll export");
    await userEvent.click(within(card).getByRole("button", { name: /Month/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Mar" }));
    await userEvent.click(within(card).getByRole("button", { name: "Export for payroll" }));

    await waitFor(() =>
      expect(sent(fetchMock, "GET", /\/payroll\/export/).map((one) => one.url)).toEqual([
        "/api/v1/staff/payroll/export?month=2026-03&format=csv",
      ]),
    );
  });
});

describe("Settings — retention", () => {
  it("shows the derived block, and saves the two periods with the revision it read", async () => {
    const fetchMock = stubStaffApi({
      settings: settings({
        retention: {
          pendingDeletions: 3,
          failingDeletions: 1,
          oldestPendingSince: "2026-10-01T12:00:00Z",
          lastErasedAt: "2026-10-10T12:00:00Z",
        },
      }),
    });
    renderRoute("/staff/settings");

    const card = await region("Retention");
    expect(figure(card, "Pending deletions")).toHaveTextContent("3");
    expect(figure(card, "Failing deletions")).toHaveTextContent("1");
    expect(within(card).getByText(/check that the object store is configured and reachable/)).toBeInTheDocument();
    expect(figure(card, "Oldest pending since")).toHaveTextContent("Oct 1, 2026");
    expect(figure(card, "Last erased")).toHaveTextContent("Oct 10, 2026");

    await retype(within(card).getByRole("textbox", { name: "Grace period (days)" }), "120");
    await userEvent.click(within(card).getByRole("button", { name: "Save periods" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/staff\/settings$/);
    expect(put.ifMatch).toBe("3");
    // The replace carries the general settings as read, so nothing else is cleared.
    expect(put.body).toEqual({
      timeZone: "Europe/Oslo",
      employeeNumberPrefix: "E-",
      nextEmployeeNumber: 8,
      pensionProvider: "Storebrand",
      retentionGraceDays: 120,
      healthRetentionDays: 365,
    });
  });

  it("says nothing is failing when no deletion has failed, and nothing was erased yet", async () => {
    stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Retention");
    expect(figure(card, "Failing deletions")).toHaveTextContent("0");
    expect(within(card).queryByText(/check that the object store/)).not.toBeInTheDocument();
    expect(figure(card, "Last erased")).toHaveTextContent("Never");
  });

  it("reloads the periods the server holds when somebody else saved the settings first", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Retention");
    await retype(within(card).getByRole("textbox", { name: "Grace period (days)" }), "120");
    // Meanwhile somebody saves the general settings: the revision moves, the periods do not.
    fetchMock.state.settings = { ...fetchMock.state.settings, injuryInsurer: "Tryg", revision: 4 };
    await userEvent.click(within(card).getByRole("button", { name: "Save periods" }));

    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    await waitFor(() => expect(within(card).getByRole("textbox", { name: "Grace period (days)" })).toHaveValue("90"));
  });

  it("refuses a period outside its bounds without asking", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Retention");
    await retype(within(card).getByRole("textbox", { name: "Grace period (days)" }), "4000");
    await retype(within(card).getByRole("textbox", { name: "Health documents (days)" }), "10");
    await userEvent.click(within(card).getByRole("button", { name: "Save periods" }));

    expect(await within(card).findByText("0 to 3650 days")).toBeInTheDocument();
    expect(within(card).getByText("30 to 3650 days")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/staff\/settings$/)).toHaveLength(0);
  });

  it("runs retention after a confirm, and says what it did in words", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    const card = await region("Retention");
    expect(within(card).getByText(/what is not finished in 30 seconds/)).toBeInTheDocument();
    await userEvent.click(within(card).getByRole("button", { name: "Run retention now" }));
    const confirm = await screen.findByRole("dialog", { name: "Run retention now?" });
    expect(within(confirm).getByText(/erases for good/)).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/retention\/run$/)).toHaveLength(0);
    await userEvent.click(within(confirm).getByRole("button", { name: "Run" }));

    expect(
      await within(card).findByText(
        "Erased: personnel administration 3, health documents 1, pay and contract 2. Deletions from the object store: 4 attempted, 4 succeeded.",
      ),
    ).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/retention\/run$/)).toHaveLength(1);
  });

  it("says when a run found nothing due", async () => {
    stubStaffApi({ retentionRun: { erased: { kinds: [] }, deletions: { attempted: 0, succeeded: 0 } } });
    renderRoute("/staff/settings");

    await userEvent.click(within(await region("Retention")).getByRole("button", { name: "Run retention now" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Run retention now?" })).getByRole("button", { name: "Run" }),
    );

    expect(
      await screen.findByText(
        "Nothing was due to be erased. Deletions from the object store: 0 attempted, 0 succeeded.",
      ),
    ).toBeInTheDocument();
  });

  it("says retention_busy in words", async () => {
    stubStaffApi({
      write: (method, path) =>
        method === "POST" && path === "/api/v1/staff/retention/run"
          ? staffProblem(409, {
              code: "retention_busy",
              title: "Retention is running",
              detail: "The retention worker holds the lease.",
            })
          : undefined,
    });
    renderRoute("/staff/settings");

    await userEvent.click(within(await region("Retention")).getByRole("button", { name: "Run retention now" }));
    await userEvent.click(
      within(await screen.findByRole("dialog", { name: "Run retention now?" })).getByRole("button", { name: "Run" }),
    );

    expect(
      await screen.findByText("The retention worker is running right now. Try again in a moment."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/holds the lease/)).not.toBeInTheDocument();
  });
});

describe("Settings — the thresholds", () => {
  const thresholdRow = async (name: string) =>
    within(await screen.findByRole("table", { name: "Thresholds" }))
      .getByText(name)
      .closest("tr") as HTMLElement;

  it("shows when a threshold was acknowledged, and acknowledges another after a confirm, for good", async () => {
    const fetchMock = stubStaffApi({ settings: settings({ thresholdsAcknowledged: { "5": "2026-03-02T12:00:00Z" } }) });
    renderRoute("/staff/settings");

    const five = await thresholdRow("5 or more employees");
    expect(five).toHaveTextContent("Acknowledged Mar 2, 2026");
    expect(within(five).queryByRole("button")).not.toBeInTheDocument();
    expect(await thresholdRow("More than 50 employees")).toHaveTextContent("ARP");
    expect(await thresholdRow("Occupational pension (OTP)")).toHaveTextContent(
      "unless the only such person is the owner",
    );

    await userEvent.click(
      within(await thresholdRow("30 or more employees")).getByRole("button", {
        name: "Acknowledge 30 or more employees",
      }),
    );
    const confirm = await screen.findByRole("dialog", { name: "Acknowledge 30 or more employees?" });
    expect(within(confirm).getByText(/cannot be undone/)).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Acknowledge" }));

    await waitFor(async () =>
      expect(await thresholdRow("30 or more employees")).toHaveTextContent("Acknowledged Oct 11, 2026"),
    );
    expect(sent(fetchMock, "POST", /\/thresholds\/30\/acknowledge$/)).toHaveLength(1);
    expect(within(await thresholdRow("30 or more employees")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("acknowledges nothing when the confirm is cancelled", async () => {
    const fetchMock = stubStaffApi();
    renderRoute("/staff/settings");

    await userEvent.click(
      within(await thresholdRow("Occupational pension (OTP)")).getByRole("button", {
        name: "Acknowledge Occupational pension (OTP)",
      }),
    );
    const confirm = await screen.findByRole("dialog", { name: "Acknowledge Occupational pension (OTP)?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(sent(fetchMock, "POST", /\/acknowledge$/)).toHaveLength(0);
  });
});

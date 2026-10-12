import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, staffProblem, stubStaffApi } from "../test/server";

const tab = (name: string) => screen.findByRole("tab", { name });

/** The parts of the uploads the fake received, as the multipart body carried them. */
const uploads = (fetchMock: StaffStub) =>
  fetchMock.actualCalls
    .filter(([url, init]) => init?.method === "POST" && /\/people\/\d+\/documents$/.test(String(url)))
    .map(([url, init]) => ({ url: String(url), form: init?.body as FormData }));

const pdf = (name = "first-aid.pdf", size?: number) => {
  const file = new File(["%PDF-1.7 a certificate"], name, { type: "application/pdf" });
  if (size !== undefined) Object.defineProperty(file, "size", { value: size });
  return file;
};

/** Opens Add document on Ada's Documents tab and answers the dialog. */
const openUpload = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Add document" }));
  return screen.findByRole("dialog", { name: "Add document" });
};

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

/** The anchor the page clicks to hand a file over, and the object URL it made. */
const catchDownloads = () => {
  const { createObjectURL, revokeObjectURL } = URL;
  URL.createObjectURL = vi.fn(() => "blob:staff");
  URL.revokeObjectURL = vi.fn();
  onTestFinished(() => {
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
  });
  return vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
};

describe("Documents tab — who sees it", () => {
  it("shows HR every document but the health ones, grouped by category", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    expect(await tab("Documents")).toHaveAttribute("aria-selected", "true");
    const contract = await region("Contract");
    expect(within(contract).getByText("employment-contract.pdf")).toBeInTheDocument();
    const certificates = await region("Certificate");
    expect(within(certificates).getByText("forklift-licence.pdf")).toBeInTheDocument();
    expect(within(certificates).getByText("førstehjelp.png")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Health" })).not.toBeInTheDocument();
    expect(screen.queryByText("sick-note.pdf")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add document" })).toBeInTheDocument();
  });

  it("shows staff:personal-data the health documents too", async () => {
    stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101?tab=documents");

    expect(within(await region("Health")).getByText("sick-note.pdf")).toBeInTheDocument();
    expect(within(await region("Health")).getByText("E · Health")).toBeInTheDocument();
  });

  it("shows the person their own, health included, and nothing to change", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/101?tab=documents");

    expect(within(await region("Health")).getByText("sick-note.pdf")).toBeInTheDocument();
    expect(within(await region("Contract")).getByText("employment-contract.pdf")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add document" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Change / })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Remove / })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download sick-note.pdf" })).toBeInTheDocument();
  });

  it("is not there for a manager of the person", async () => {
    // Ada, linked, manages Grace.
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=documents");

    expect(await tab("Profile")).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("tab", { name: "Documents" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/documents$/)).toHaveLength(0);
  });

  it("is not there for a colleague holding the directory", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    renderRoute("/staff/people/101?tab=documents");

    expect(await tab("Profile")).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("tab", { name: "Documents" })).not.toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/documents$/)).toHaveLength(0);
  });
});

describe("Documents tab — the list", () => {
  it("badges each category with its class, and flags a validity that has passed", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    expect(within(await region("Contract")).getByText("A · Pay and contract")).toBeInTheDocument();
    const certificates = await region("Certificate");
    expect(within(certificates).getByText("D · Personnel")).toBeInTheDocument();
    const expired = within(certificates).getByRole("row", { name: /forklift-licence\.pdf/ });
    expect(within(expired).getByText("Jun 30, 2026")).toBeInTheDocument();
    expect(within(expired).getByText("Expired")).toBeInTheDocument();
    const valid = within(certificates).getByRole("row", { name: /førstehjelp\.png/ });
    expect(within(valid).getByText("May 1, 2027")).toBeInTheDocument();
    expect(within(valid).queryByText("Expired")).not.toBeInTheDocument();
    expect(within(valid).getByText("2.3 MB")).toBeInTheDocument();
    const contract = within(await region("Contract")).getByRole("row", { name: /employment-contract\.pdf/ });
    expect(within(contract).getByText("Dec 15, 2024")).toBeInTheDocument();
  });

  it("downloads a document through the API and saves it under the server's name", async () => {
    const click = catchDownloads();
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    await userEvent.click(await screen.findByRole("button", { name: "Download førstehjelp.png" }));

    await waitFor(() => expect(click).toHaveBeenCalled());
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe("førstehjelp.png");
    expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/api/v1/staff/documents/503"))).toBe(true);
    await waitFor(() => expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:staff"));
  });

  it("changes a document's dates with the revision it read", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    await userEvent.click(await screen.findByRole("button", { name: "Change forklift-licence.pdf" }));
    const dialog = await screen.findByRole("dialog", { name: "Change forklift-licence.pdf" });
    await typeDate(within(dialog).getByRole("textbox", { name: "Valid to" }), "Jun 30, 2028");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/documents\/502$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/documents\/502$/);
    expect(put.ifMatch).toBe("1");
    expect(put.body).toEqual({ categoryId: 3, validTo: "2028-06-30" });
    const row = await within(await region("Certificate")).findByRole("row", { name: /forklift-licence\.pdf/ });
    expect(await within(row).findByText("Jun 30, 2028")).toBeInTheDocument();
    expect(within(row).queryByText("Expired")).not.toBeInTheDocument();
  });

  it("explains on a contract that the signing date reaches the employment", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    await userEvent.click(await screen.findByRole("button", { name: "Change employment-contract.pdf" }));
    const dialog = await screen.findByRole("dialog", { name: "Change employment-contract.pdf" });
    expect(within(dialog).getByRole("textbox", { name: "Signed on" })).toHaveValue("Dec 15, 2024");
    expect(within(dialog).getByText(/sets the employment's signing date/)).toBeInTheDocument();
  });

  it("removes a document for good after asking", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    await userEvent.click(await screen.findByRole("button", { name: "Remove forklift-licence.pdf" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove forklift-licence.pdf?" });
    expect(within(confirm).getByText(/removed for good/)).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/documents\/502$/)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByText("forklift-licence.pdf")).not.toBeInTheDocument());
    expect(screen.getByText("førstehjelp.png")).toBeInTheDocument();
  });
});

describe("Documents tab — filing a document", () => {
  it("sends the file and the category as multipart, and lists it after", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf());
    expect(within(dialog).getByText("first-aid.pdf")).toBeInTheDocument();
    await choose(dialog, /Category/, "Certificate");
    await typeDate(within(dialog).getByRole("textbox", { name: "Valid to" }), "Jan 31, 2028");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    await waitFor(() => expect(uploads(fetchMock)).toHaveLength(1));
    const [{ url, form }] = uploads(fetchMock);
    expect(url).toMatch(/\/api\/v1\/staff\/people\/101\/documents$/);
    expect((form.get("file") as File).name).toBe("first-aid.pdf");
    expect(form.get("categoryId")).toBe("3");
    expect(form.get("validTo")).toBe("2028-01-31");
    expect(form.get("signedAt")).toBeNull();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add document" })).not.toBeInTheDocument());
    expect(await within(await region("Certificate")).findByText("first-aid.pdf")).toBeInTheDocument();
  });

  it("refuses a file over 10 MiB before sending anything", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf("scan.pdf", 11 * 1024 * 1024));
    expect(await within(dialog).findByText("scan.pdf is larger than 10 MiB")).toBeInTheDocument();
    await choose(dialog, /Category/, "Certificate");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    expect(within(dialog).getByText("scan.pdf is larger than 10 MiB")).toBeInTheDocument();
    expect(uploads(fetchMock)).toHaveLength(0);
  });

  it("puts the server's type refusal on the file", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/documents")
          ? staffProblem(400, {
              code: "document_type_unsupported",
              title: "Invalid document",
              errors: { file: ["A document must be a PDF, JPEG, PNG or HEIC file, and its contents must match"] },
            })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf("renamed.pdf"));
    await choose(dialog, /Category/, "Certificate");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    const file = within(dialog).getByRole("group", { name: "File" });
    expect(await within(file).findByText(/must be a PDF, JPEG, PNG or HEIC file/)).toBeInTheDocument();
  });

  it("puts category_restricted on the category, in the reader's words", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/documents")
          ? staffProblem(403, {
              code: "category_restricted",
              title: "Restricted category",
              detail: "staff:personal-data",
            })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf());
    await choose(dialog, /Category/, "Certificate");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    expect(
      await within(dialog).findByText("Only those who may see health documents file, move or remove them"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("combobox", { name: /Category/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("says the 200 cap as a banner", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/documents")
          ? staffProblem(409, {
              code: "document_count_exceeded",
              title: "Too many documents",
              detail: "A person has at most 200 documents.",
            })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf());
    await choose(dialog, /Category/, "Certificate");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    const banner = await within(dialog).findByRole("alert");
    expect(banner).toHaveTextContent("This person already has 200 documents");
  });

  it("says an unreachable store as a banner", async () => {
    stubStaffApi({
      role: "manage",
      write: (method, path) =>
        method === "POST" && path.endsWith("/documents")
          ? staffProblem(503, { code: "storage_unavailable", title: "Document storage is unavailable" })
          : undefined,
    });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf());
    await choose(dialog, /Category/, "Certificate");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("The document store could not be reached");
  });

  it("offers HR no health category, nor an archived one", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.click(within(dialog).getByRole("combobox", { name: /Category/ }));
    const options = (await screen.findAllByRole("option")).map((option) => option.textContent);
    expect(options).toEqual(["Contract", "Attest", "Certificate", "Correspondence", "Other", "Training record"]);
  });

  it("offers staff:personal-data the health category", async () => {
    stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.click(within(dialog).getByRole("combobox", { name: /Category/ }));
    expect(await screen.findByRole("option", { name: "Health" })).toBeInTheDocument();
  });

  it("asks for the signing date on a contract and says what it sets", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), pdf("contract.pdf"));
    await choose(dialog, /Category/, "Contract");
    expect(within(dialog).getByText(/sets the employment's signing date/)).toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "Signed on" }), "Oct 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Upload" }));

    await waitFor(() => expect(uploads(fetchMock)).toHaveLength(1));
    expect(uploads(fetchMock)[0].form.get("signedAt")).toBe("2026-10-01");
    expect(uploads(fetchMock)[0].form.get("categoryId")).toBe("1");
  });
});

describe("Documents tab — on a phone", () => {
  const original = window.matchMedia;
  afterEach(() => {
    window.matchMedia = original;
  });

  it("offers the camera", async () => {
    window.matchMedia = vi.fn((query: string) => ({
      matches: query.includes("pointer: coarse"),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=documents");

    const dialog = await openUpload();
    const camera = within(dialog).getByLabelText("Take a photo");
    expect(camera).toHaveAttribute("capture", "environment");
    expect(camera).toHaveAttribute("accept", "image/*");
  });
});

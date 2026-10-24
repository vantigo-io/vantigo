import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { validateMyPageSearch } from "../lib/search";
import { personCompetences } from "../test/competence-fixtures";
import { awaitDownload, choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, stubStaffApi } from "../test/server";

const typeDate = async (field: HTMLElement, text: string) => {
  await retype(field, text);
  await userEvent.tab();
};

/** One competence row, by the competence's name. */
const row = (name: string) => screen.findByRole("article", { name });

/** The evidence uploads the fake received, with their multipart bodies. */
const evidenceUploads = (fetchMock: StaffStub) =>
  fetchMock.actualCalls
    .filter(([url, init]) => init?.method === "POST" && /\/person-competences\/\d+\/evidence$/.test(String(url)))
    .map(([url, init]) => ({ url: String(url), form: init?.body as FormData }));

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

const openAdd = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Add competence" }));
  return screen.findByRole("dialog", { name: "Add competence" });
};

describe("My page — my competences", () => {
  it("shows my competences by category, a verified certificate read-only but for its renewal", async () => {
    stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    expect(await screen.findByRole("tab", { name: "Competence" })).toHaveAttribute("aria-selected", "true");
    const certificates = await region("Certificate");
    const fse = await within(certificates).findByRole("article", { name: "Electrical safety (FSE)" });
    expect(within(fse).getByText("Verified")).toBeInTheDocument();
    expect(within(fse).getByText("Expiring")).toBeInTheDocument();
    expect(within(fse).getByText("Valid to Nov 1, 2026 · 21 days left")).toBeInTheDocument();
    expect(within(fse).getByText("Verified by Hedy Lamarr on Nov 3, 2025 · The evidence")).toBeInTheDocument();
    expect(within(fse).getByText("FSE-2025-17")).toBeInTheDocument();
    expect(within(fse).getByRole("button", { name: "Download fse-course.png" })).toBeInTheDocument();
    // Verified: mine to renew, not to change, remove or verify.
    expect(within(fse).getByRole("button", { name: "Renewed" })).toBeInTheDocument();
    for (const action of ["Edit", "Remove", "Verify", "Add evidence"])
      expect(within(fse).queryByRole("button", { name: action })).not.toBeInTheDocument();

    const welding = within(await region("Trade")).getByRole("article", { name: "Welding" });
    expect(within(welding).getByText("independent")).toBeInTheDocument();
    expect(within(welding).getByText("Declared")).toBeInTheDocument();
    for (const action of ["Edit", "Add evidence", "Remove"])
      expect(within(welding).getByRole("button", { name: action })).toBeInTheDocument();
    // A skill has no expiry: edited, never renewed.
    for (const action of ["Verify", "Renewed"])
      expect(within(welding).queryByRole("button", { name: action })).not.toBeInTheDocument();
  });

  it("tells the day of a verification in the business time zone, not UTC's", async () => {
    // 00:30 on Nov 3 in Oslo is still Nov 2 in UTC.
    stubStaffApi({
      role: "access",
      me: 101,
      personCompetences: personCompetences().map((one) =>
        one.id === 3001 ? { ...one, verifiedAt: "2025-11-02T23:30:00Z" } : one,
      ),
    });
    renderRoute("/staff/me?tab=competence");

    const fse = await row("Electrical safety (FSE)");
    expect(within(fse).getByText("Verified by Hedy Lamarr on Nov 3, 2025 · The evidence")).toBeInTheDocument();
  });

  it("puts HR, on their own record, first the person: no change or removal of their verified row", async () => {
    stubStaffApi({ role: "manage", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const fse = await row("Electrical safety (FSE)");
    expect(within(fse).getByRole("button", { name: "Renewed" })).toBeInTheDocument();
    for (const action of ["Edit", "Remove", "Verify"])
      expect(within(fse).queryByRole("button", { name: action })).not.toBeInTheDocument();
    // Their own declaration stays theirs to change and remove.
    const welding = await row("Welding");
    for (const action of ["Edit", "Remove"])
      expect(within(welding).getByRole("button", { name: action })).toBeInTheDocument();

    await userEvent.click(within(welding).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove Welding?" });
    // The chain is HR's on somebody else's record alone.
    expect(within(confirm).queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("offers a skill no expiry, to add or to edit", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const dialog = await openAdd();
    await choose(dialog, /^Competence/, "Hot work");
    expect(within(dialog).getByRole("textbox", { name: "Valid to" })).toBeInTheDocument();
    await choose(dialog, /^Competence/, "Excel");
    expect(within(dialog).queryByRole("textbox", { name: "Valid to" })).not.toBeInTheDocument();
    await typeDate(within(dialog).getByRole("textbox", { name: "Issued" }), "Oct 1, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/101\/competences$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/people\/101\/competences$/)[0].body).toEqual({
      competenceId: 2006,
      issuedOn: "2026-10-01",
    });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add competence" })).not.toBeInTheDocument());

    await userEvent.click(within(await row("Welding")).getByRole("button", { name: "Edit" }));
    const edit = await screen.findByRole("dialog", { name: "Change Welding" });
    expect(within(edit).getByRole("textbox", { name: "Issued" })).toBeInTheDocument();
    expect(within(edit).queryByRole("textbox", { name: "Valid to" })).not.toBeInTheDocument();
  });

  it("declares a skill, putting the server's level_required on the level", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const dialog = await openAdd();
    // Neither hidden nor archived competences are offered.
    await userEvent.click(within(dialog).getByRole("combobox", { name: /^Competence/ }));
    expect(screen.queryByRole("option", { name: "First aid" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Lotus 1-2-3" })).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("option", { name: "English" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("Choose a level: this competence has one.")).toBeInTheDocument();
    await choose(dialog, /^Level/, "experienced");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/101\/competences$/)).toHaveLength(2));
    expect(sent(fetchMock, "POST", /\/people\/101\/competences$/)[1].body).toEqual({ competenceId: 2005, level: 3 });
    const english = await row("English");
    expect(within(english).getByText("Declared")).toBeInTheDocument();
    expect(within(english).getByText("experienced")).toBeInTheDocument();
  });

  it("files the evidence chosen with a declaration as a second request", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    const dialog = await openAdd();
    await choose(dialog, /^Competence/, "Hot work");
    await typeDate(within(dialog).getByRole("textbox", { name: "Issued" }), "Oct 1, 2026");
    // The validity of five years proposes the expiry.
    expect(within(dialog).getByRole("textbox", { name: "Valid to" })).toHaveValue("Oct 1, 2031");
    const photo = new File(["\xff\xd8\xff a photo"], "hot-work.jpg", { type: "image/jpeg" });
    await userEvent.upload(within(dialog).getByLabelText("Choose a file"), photo);
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(evidenceUploads(fetchMock)).toHaveLength(1));
    const [post] = sent(fetchMock, "POST", /\/people\/101\/competences$/);
    expect(post.body).toEqual({ competenceId: 2002, issuedOn: "2026-10-01", validTo: "2031-10-01" });
    const [{ url, form }] = evidenceUploads(fetchMock);
    const created = fetchMock.state.personCompetences.find((one) => one.competence.key === "varme-arbeider");
    expect(url).toMatch(new RegExp(`/person-competences/${created?.id}/evidence$`));
    expect((form.get("file") as File).name).toBe("hot-work.jpg");
    expect(within(await row("Hot work")).getByRole("button", { name: "Download hot-work.jpg" })).toBeInTheDocument();
    expect(fetchMock.state.documents[0]).toMatchObject({ personId: 101, category: { key: "certificate" } });
  });

  it("renews a certificate as a new row, keeping the old one as history", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    await userEvent.click(within(await row("Electrical safety (FSE)")).getByRole("button", { name: "Renewed" }));
    const dialog = await screen.findByRole("dialog", { name: "Electrical safety (FSE) renewed" });
    await typeDate(within(dialog).getByRole("textbox", { name: "Issued" }), "Oct 5, 2026");
    expect(within(dialog).getByRole("textbox", { name: "Valid to" })).toHaveValue("Oct 5, 2027");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/person-competences\/3001\/renew$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/person-competences\/3001\/renew$/)[0].body).toEqual({
      issuedOn: "2026-10-05",
      validTo: "2027-10-05",
      issuer: "Fjord Systems AS",
    });
    await waitFor(() => expect(screen.getAllByRole("article", { name: "Electrical safety (FSE)" })).toHaveLength(1));
    const renewed = await row("Electrical safety (FSE)");
    expect(await within(renewed).findByText("Declared")).toBeInTheDocument();
    expect(within(renewed).getByText(/^Valid to Oct 5, 2027/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("switch", { name: "Show history" }));
    await waitFor(() => expect(screen.getAllByRole("article", { name: "Electrical safety (FSE)" })).toHaveLength(2));
    const [, old] = screen.getAllByRole("article", { name: "Electrical safety (FSE)" });
    expect(within(old).getByText("Renewed since")).toBeInTheDocument();
    expect(within(old).queryByRole("button", { name: "Renewed" })).not.toBeInTheDocument();
  });

  it("removes my own declared competence", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    await userEvent.click(within(await row("Welding")).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove Welding?" });
    // The chain is HR's alone.
    expect(within(confirm).queryByRole("checkbox")).not.toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/person-competences\/3002$/)).toHaveLength(1));
    expect(sent(fetchMock, "DELETE", /\/person-competences\/3002$/)[0].url).not.toContain("chain");
    await waitFor(() => expect(screen.queryByRole("article", { name: "Welding" })).not.toBeInTheDocument());
  });

  it("brings the previous row back when I remove a renewal", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/me?tab=competence");

    await userEvent.click(within(await row("Electrical safety (FSE)")).getByRole("button", { name: "Renewed" }));
    const dialog = await screen.findByRole("dialog", { name: "Electrical safety (FSE) renewed" });
    await typeDate(within(dialog).getByRole("textbox", { name: "Issued" }), "Oct 5, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    const renewed = await row("Electrical safety (FSE)");
    expect(await within(renewed).findByText("Declared")).toBeInTheDocument();

    await userEvent.click(within(renewed).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove Electrical safety (FSE)?" });
    expect(
      within(confirm).getByText(
        "Removing a renewed row brings the previous one back; HR can remove the whole history instead.",
      ),
    ).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/person-competences\/\d+$/)).toHaveLength(1));
    const previous = await row("Electrical safety (FSE)");
    expect(await within(previous).findByText("Verified")).toBeInTheDocument();
    expect(within(previous).getByText("Valid to Nov 1, 2026 · 21 days left")).toBeInTheDocument();
    expect(screen.getAllByRole("article", { name: "Electrical safety (FSE)" })).toHaveLength(1);
  });

  it("opens Add competence on arrival (the spotlight's), and forgets it on close", async () => {
    stubStaffApi({ role: "access", me: 101 });
    const { router } = renderRoute("/staff/me?tab=competence&create=competence");

    const dialog = await screen.findByRole("dialog", { name: "Add competence" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add competence" })).not.toBeInTheDocument());
    expect(router.state.location.search).toEqual({ tab: "competence" });
    expect(validateMyPageSearch({ tab: "competence", create: "competence" })).toEqual({
      tab: "competence",
      create: "competence",
    });
  });
});

describe("The person page — competence", () => {
  it("lets the manager verify a declaration with the level agreed, the basis and a note", async () => {
    // Ada, linked, manages Grace.
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=competence");

    const english = await row("English");
    expect(within(english).getByText("Declared")).toBeInTheDocument();
    await userEvent.click(within(english).getByRole("button", { name: "Verify" }));
    const dialog = await screen.findByRole("dialog", { name: "Verify English" });
    expect(within(dialog).getByRole("combobox", { name: /^Level/ })).toHaveValue("experienced");
    await choose(dialog, /^Level/, "expert");
    await userEvent.click(within(dialog).getByRole("radio", { name: "Seen in the work" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Led the kickoff in English");
    await userEvent.click(within(dialog).getByRole("button", { name: "Verify" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/person-competences\/3003\/verify$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/person-competences\/3003\/verify$/)[0].body).toEqual({
      basis: "observed",
      level: 4,
      note: "Led the kickoff in English",
    });
    const verified = await row("English");
    expect(await within(verified).findByText("Verified")).toBeInTheDocument();
    expect(
      within(verified).getByText("Verified by Ada Lovelace on Oct 11, 2026 · Seen in the work"),
    ).toBeInTheDocument();
    expect(within(verified).getByText("expert")).toBeInTheDocument();
  });

  it("offers a manager adding a competence on a report no evidence, which is not theirs to file", async () => {
    const fetchMock = stubStaffApi({ role: "access", me: 101 });
    renderRoute("/staff/people/102?tab=competence");

    // Grace's English has no evidence; the manager is offered none to add.
    expect(within(await row("English")).queryByRole("button", { name: "Add evidence" })).not.toBeInTheDocument();
    const dialog = await openAdd();
    expect(within(dialog).queryByRole("group", { name: "Evidence" })).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Choose a file")).not.toBeInTheDocument();
    await choose(dialog, /^Competence/, "Hot work");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/102\/competences$/)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add competence" })).not.toBeInTheDocument());
    expect(evidenceUploads(fetchMock)).toHaveLength(0);
    // The fake refuses a manager's upload as the server does.
    const created = fetchMock.state.personCompetences.find(
      (one) => one.personId === 102 && one.competence.key === "varme-arbeider",
    );
    const form = new FormData();
    form.append("file", new File(["%PDF"], "course.pdf", { type: "application/pdf" }));
    const refused = await fetch(`http://localhost/api/v1/staff/person-competences/${created?.id}/evidence`, {
      method: "POST",
      body: form,
    });
    expect(refused.status).toBe(403);
  });

  it("lets the manager download the evidence on a row they read, through the row", async () => {
    const click = catchDownloads();
    const fetchMock = stubStaffApi({
      role: "access",
      me: 101,
      personCompetences: personCompetences().map((one) =>
        one.id === 3003
          ? {
              ...one,
              evidence: { documentId: 599, fileName: "toefl.pdf", contentType: "application/pdf", sizeBytes: 90_000 },
            }
          : one,
      ),
    });
    renderRoute("/staff/people/102?tab=competence");

    await userEvent.click(within(await row("English")).getByRole("button", { name: "Download toefl.pdf" }));

    await awaitDownload(click);
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe("toefl.pdf");
    expect(
      fetchMock.actualCalls.some(([url]) => String(url).endsWith("/api/v1/staff/person-competences/3003/evidence")),
    ).toBe(true);
    // Not the archive's download, which is the person's and HR's alone.
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/staff/documents/"))).toBe(false);
  });

  it("refuses a colleague the evidence with the unknown row's 404", async () => {
    stubStaffApi({ role: "view" });
    const response = await fetch("http://localhost/api/v1/staff/person-competences/3001/evidence");
    expect(response.status).toBe(404);
  });

  it("shows a colleague the competences, levels and validity alone, with nothing to do", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people/101?tab=competence");

    const fse = await row("Electrical safety (FSE)");
    expect(within(fse).getByText("Verified")).toBeInTheDocument();
    expect(within(fse).getByText("Valid to Nov 1, 2026 · 21 days left")).toBeInTheDocument();
    expect(within(fse).queryByText(/Verified by/)).not.toBeInTheDocument();
    expect(within(fse).queryByText("FSE-2025-17")).not.toBeInTheDocument();
    expect(within(fse).queryByRole("button", { name: /Download/ })).not.toBeInTheDocument();
    const competences = await region("Competences");
    // The history switch alone: no action on any row, and no Add competence.
    expect(within(competences).queryAllByRole("button")).toHaveLength(0);
  });

  it("gives HR the chain on removal", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people/101?tab=competence");

    await userEvent.click(within(await row("Electrical safety (FSE)")).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove Electrical safety (FSE)?" });
    await userEvent.click(within(confirm).getByRole("checkbox", { name: /Remove the whole history/ }));
    await userEvent.click(within(confirm).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/person-competences\/3001\?chain=true$/)).toHaveLength(1));
  });
});

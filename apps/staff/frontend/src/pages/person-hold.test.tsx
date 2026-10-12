import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { adaRecord, erasedPerson, heldPeople, withErased } from "../test/fixtures";
import { awaitDownload, region, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { staffProblem, stubStaffApi } from "../test/server";

/**
 * The ceiling for a chain of round trips — a write, the refetch it causes,
 * the render — which the host's suite, at testing-library's one-second
 * default, does not give on a loaded runner.
 */
const slow = { timeout: 5000 };
const heading = (name: string) => screen.findByRole("heading", { name, level: 2 });
const confirmIn = async (title: RegExp, button: string) =>
  userEvent.click(within(await screen.findByRole("dialog", { name: title })).getByRole("button", { name: button }));

describe("Person page — the legal hold", () => {
  it("offers staff:personal-data to put a person under a hold, with a reason", async () => {
    const fetchMock = stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101");

    await heading("Ada Lovelace");
    expect(screen.queryByText("Under legal hold")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Put under legal hold" }));
    const dialog = await screen.findByRole("dialog", { name: "Put Ada Lovelace under a legal hold" });
    // The reason is required, and said so before anything is sent.
    await userEvent.click(within(dialog).getByRole("button", { name: "Set the legal hold" }));
    expect(await within(dialog).findByText("Give the reason for the hold")).toBeInTheDocument();
    expect(sent(fetchMock, "PUT", /\/legal-hold$/)).toHaveLength(0);

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "  Wage claim before the court ");
    await userEvent.click(within(dialog).getByRole("button", { name: "Set the legal hold" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/legal-hold$/)).toHaveLength(1), slow);
    const [request] = sent(fetchMock, "PUT", /\/legal-hold$/);
    expect(request.url).toBe("/api/v1/staff/people/101/legal-hold");
    expect(request.body).toEqual({ held: true, reason: "Wage claim before the court" });
    const hold = await screen.findByRole("alert", { name: "Under legal hold" }, slow);
    expect(hold).toHaveTextContent("Wage claim before the court");
    expect(hold).toHaveTextContent("Since Oct 11, 2026");
    expect(screen.getByRole("button", { name: "Lift the legal hold" })).toBeInTheDocument();
  });

  it("lifts a hold after asking, sending no reason", async () => {
    const fetchMock = stubStaffApi({ role: "personalData", people: heldPeople() });
    renderRoute("/staff/people/101");

    const hold = await screen.findByRole("alert", { name: "Under legal hold" });
    expect(hold).toHaveTextContent("Wage claim before the court");
    expect(hold).toHaveTextContent("Since Sep 2, 2026");
    await userEvent.click(screen.getByRole("button", { name: "Lift the legal hold" }));
    await confirmIn(/Lift the legal hold on Ada Lovelace/, "Lift the legal hold");

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/legal-hold$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "PUT", /\/legal-hold$/)[0].body).toEqual({ held: false });
    await waitFor(
      () => expect(screen.queryByRole("alert", { name: "Under legal hold" })).not.toBeInTheDocument(),
      slow,
    );
    expect(screen.getByRole("button", { name: "Put under legal hold" })).toBeInTheDocument();
  });

  it("says a refusal in words, on the reason when it names it", async () => {
    stubStaffApi({
      role: "personalData",
      write: (method, path) =>
        method === "PUT" && path.endsWith("/legal-hold")
          ? staffProblem(400, {
              title: "Invalid legal hold",
              errors: { reason: ["A legal hold states its reason, 1 to 500 characters"] },
            })
          : undefined,
    });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Put under legal hold" }));
    const dialog = await screen.findByRole("dialog", { name: "Put Ada Lovelace under a legal hold" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "Claim");
    await userEvent.click(within(dialog).getByRole("button", { name: "Set the legal hold" }));

    expect(
      await within(dialog).findByText("A legal hold states its reason, 1 to 500 characters", {}, slow),
    ).toBeInTheDocument();
  });

  it("shows staff:manage the hold and nothing to change", async () => {
    stubStaffApi({ role: "manage", people: heldPeople() });
    renderRoute("/staff/people/101");

    expect(await screen.findByRole("alert", { name: "Under legal hold" })).toHaveTextContent(
      "Wage claim before the court",
    );
    expect(screen.queryByRole("button", { name: "Lift the legal hold" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Put under legal hold" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Download personal data" })).not.toBeInTheDocument();
  });

  it("shows a colleague and a manager neither the hold nor its controls", async () => {
    stubStaffApi({ role: "view", people: heldPeople() });
    renderRoute("/staff/people/101");
    await heading("Ada Lovelace");
    expect(screen.queryByText("Under legal hold")).not.toBeInTheDocument();
    expect(screen.queryByText("Wage claim before the court")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /legal hold/ })).not.toBeInTheDocument();
  });
});

describe("Person page — the personal-data file", () => {
  it("downloads the complete file under the server's name, after saying the download is recorded", async () => {
    const { createObjectURL, revokeObjectURL } = URL;
    URL.createObjectURL = vi.fn(() => "blob:personal-data");
    URL.revokeObjectURL = vi.fn();
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const fetchMock = stubStaffApi({ role: "personalData" });
    renderRoute("/staff/people/101");

    await userEvent.click(await screen.findByRole("button", { name: "Download personal data" }));
    const dialog = await screen.findByRole("dialog", { name: "Download the personal data of Ada Lovelace" });
    expect(dialog).toHaveTextContent(/recorded in the person's history with your name/);
    await userEvent.click(within(dialog).getByRole("button", { name: "Download" }));

    await awaitDownload(click);
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe("staff-E-1-personal-data.json");
    expect(fetchMock.actualCalls.some(([url]) => String(url) === "/api/v1/staff/people/101/personal-data")).toBe(true);
  });
});

describe("Person page — an erased record", () => {
  it("says what the erase left and what still may be done", async () => {
    stubStaffApi({ role: "personalData", ...withErased() });
    renderRoute("/staff/people/106");

    await heading("Charles Babbage");
    const banner = await screen.findByRole("alert", { name: "This record was erased on Jul 1, 2025" });
    expect(banner).toHaveTextContent(/The employments, the history and the pay and contract documents remain/);
    expect(banner).toHaveTextContent(/Rehire on the Employments tab/);
    expect(screen.getByText("Erased")).toBeInTheDocument();
  });

  it("offers none of the record's changes, the unlink kept", async () => {
    stubStaffApi({
      role: "personalData",
      ...withErased(
        erasedPerson(
          {},
          { user: { id: "8d5c6a3e-0000-4000-8000-000000000106", displayName: "Charles at Fjord", active: true } },
        ),
      ),
    });
    renderRoute("/staff/people/106");

    await region("Work");
    await region("Next of kin");
    await region("Roles");
    const link = await region("Self-service");
    for (const name of [
      "Edit details",
      "Edit next of kin",
      "Edit roles",
      "Set the national identity number",
      "Set the bank account",
      "Link",
    ])
      expect(screen.queryByRole("button", { name }), name).not.toBeInTheDocument();
    expect(within(link).getByRole("button", { name: "Unlink" })).toBeInTheDocument();
  });

  it("offers no link to a user on an erased record linked to nobody", async () => {
    stubStaffApi({ role: "manage", ...withErased() });
    renderRoute("/staff/people/106");

    const link = await region("Self-service");
    expect(within(link).getByText("Not linked to a user")).toBeInTheDocument();
    expect(within(link).queryByRole("combobox")).not.toBeInTheDocument();
    expect(within(link).queryByRole("button", { name: "Link" })).not.toBeInTheDocument();
  });

  it("keeps the employments to read and offers the rehire alone", async () => {
    const fetchMock = stubStaffApi({ role: "manage", ...withErased() });
    renderRoute("/staff/people/106?tab=employments");

    const card = await region("Employment E-6-1");
    for (const name of ["New terms", "Edit employment", "End employment", "Reopen", "Correct", "Delete"])
      expect(within(card).queryByRole("button", { name }), name).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Rehire" }));

    const dialog = await screen.findByRole("dialog", { name: "Rehire Charles Babbage" });
    expect(dialog).toHaveTextContent(/opens the erased record again under the same employee number/);
    expect(dialog).toHaveTextContent(/E-6-2/);
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Employment type" }));
    await userEvent.click(await screen.findByRole("option", { name: "Permanent (fast)" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Percentage of a full position" }), "100");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save employment" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/people\/106\/employments$/)).toHaveLength(1), slow);
    await waitFor(
      () => expect(screen.queryByRole("alert", { name: /This record was erased/ })).not.toBeInTheDocument(),
      slow,
    );
    expect(await screen.findByRole("region", { name: "Employment E-6-2" }, slow)).toBeInTheDocument();
  });

  it("keeps the history and the documents to read, with nothing to file", async () => {
    stubStaffApi({
      role: "personalData",
      ...withErased(),
      events: {
        106: [
          {
            id: 460,
            kind: "erased",
            at: "2025-07-01T02:00:00Z",
            actor: { kind: "system", displayName: "Staff" },
            payload: { kinds: [{ kind: "D", count: 4 }] },
          },
        ],
      },
      documents: [
        {
          id: 560,
          personId: 106,
          employmentId: 206,
          fileName: "babbage-contract.pdf",
          contentType: "application/pdf",
          sizeBytes: 90_000,
          uploadedAt: "2019-02-01T10:00:00Z",
          revision: 1,
          category: {
            id: 1,
            key: "contract",
            names: { en: "Contract", nb: "Arbeidsavtale" },
            class: "A",
            restricted: false,
          },
        },
      ],
    });
    const { router } = renderRoute("/staff/people/106?tab=history");

    expect(await screen.findByText("Erased under the retention rules")).toBeInTheDocument();
    await router.navigate({ to: "/staff/people/106", search: { tab: "documents" } });
    expect(await screen.findByRole("button", { name: "Download babbage-contract.pdf" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add document" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^(Change|Remove) / })).not.toBeInTheDocument();
  });
});

describe("Person page — the reassessment", () => {
  it("records the reassessment after asking, then says when", async () => {
    const fetchMock = stubStaffApi({ role: "personalData", ...withErased() });
    renderRoute("/staff/people/106");

    await screen.findByRole("alert", { name: /This record was erased/ });
    await userEvent.click(screen.getByRole("button", { name: "Mark as reassessed" }));
    await confirmIn(/Record the reassessment of Charles Babbage/, "Mark as reassessed");

    await waitFor(() => expect(sent(fetchMock, "POST", /\/reassessed$/)).toHaveLength(1), slow);
    expect(sent(fetchMock, "POST", /\/reassessed$/)[0].url).toBe("/api/v1/staff/people/106/reassessed");
    expect(await screen.findByText("Reassessed on Oct 11, 2026.", {}, slow)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark as reassessed" })).not.toBeInTheDocument();
  });

  it("says in words when the reassessment is not due yet", async () => {
    stubStaffApi({
      role: "personalData",
      ...withErased(),
      write: (method, path) =>
        method === "POST" && path.endsWith("/reassessed")
          ? staffProblem(409, {
              code: "reassessment_not_due",
              title: "Reassessment not due",
              detail: "reassessment_not_due",
            })
          : undefined,
    });
    renderRoute("/staff/people/106");

    await userEvent.click(await screen.findByRole("button", { name: "Mark as reassessed" }));
    await confirmIn(/Record the reassessment of Charles Babbage/, "Mark as reassessed");

    expect(await screen.findByText(/twelve months after the last employment ended/, {}, slow)).toBeInTheDocument();
    expect(screen.queryByText("reassessment_not_due")).not.toBeInTheDocument();
  });

  it("shows staff:manage that a reassessment was recorded, with nothing to press", async () => {
    stubStaffApi({
      role: "manage",
      ...withErased(erasedPerson({}, { reassessedAt: "2026-04-02T09:00:00Z" })),
    });
    renderRoute("/staff/people/106");

    expect(await screen.findByText("Reassessed on Apr 2, 2026.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark as reassessed" })).not.toBeInTheDocument();
  });

  it("offers no reassessment on a record that is not erased", async () => {
    stubStaffApi({ role: "personalData", people: [{ ...erasedPerson(), erasedAt: undefined, record: adaRecord() }] });
    renderRoute("/staff/people/106");

    await heading("Charles Babbage");
    expect(screen.queryByRole("button", { name: "Mark as reassessed" })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert", { name: /This record was erased/ })).not.toBeInTheDocument();
  });
});

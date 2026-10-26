import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { addDays } from "../lib/weeks";
import { allocations, allocationWithTerms, HANDOVER, RELEASE, TODAY } from "../test/fixtures";
import { choose, retype, sent, shownDate } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { planningProblem, stubPlanningApi } from "../test/server";

/** The sheet's terms form as it is now: a save or a clear remounts it at the terms' new revision. */
const termsForm = () =>
  within(screen.getByRole("dialog", { name: "Change allocation" })).getByRole("form", { name: "Terms" });

/** Opens Ada's row on the harbour app's plan in the allocation sheet, and answers its terms form. */
const openTerms = async () => {
  renderRoute("/planning/projects/100");
  const row = (
    await within(await screen.findByRole("table", { name: "Allocations" })).findByText("Ada Lovelace")
  ).closest("tr") as HTMLElement;
  await userEvent.click(within(row).getByRole("button", { name: "Actions for Ada Lovelace" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
  const sheet = await screen.findByRole("dialog", { name: "Change allocation" });
  return within(sheet).getByRole("form", { name: "Terms" });
};

const withTerms = () => [allocationWithTerms(), ...allocations().slice(1)];

describe("AllocationSheet — the terms", () => {
  it("sets terms on a row without any, sending no If-Match, and shows the dates they derive", async () => {
    const fetchMock = stubPlanningApi();
    const terms = await openTerms();
    expect(within(terms).queryByRole("button", { name: "Clear terms" })).not.toBeInTheDocument();

    await userEvent.type(within(terms).getByRole("textbox", { name: "Notice period, count" }), "2");
    await choose(terms, "Notice period, unit", "weeks");
    await userEvent.type(within(terms).getByRole("textbox", { name: /^Handover days/ }), "5");
    const until = addDays(TODAY, 200);
    await userEvent.type(within(terms).getByRole("textbox", { name: "Non-compete until" }), until);
    await choose(terms, "Add a customer", "Bergen Havn");
    expect(within(terms).getByRole("list", { name: "Customers" })).toHaveTextContent("Bergen Havn");
    await userEvent.click(within(terms).getByRole("switch", { name: "Replacement needs the customer's approval" }));
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/allocations\/501\/terms$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/allocations\/501\/terms$/);
    expect(put.ifMatch).toBeNull();
    expect(put.body).toEqual({
      noticeCount: 2,
      noticeUnit: "weeks",
      replacementNeedsApproval: true,
      handoverDays: 5,
      nonCompeteUntil: until,
      nonCompeteCustomerIds: [8],
    });
    expect(await screen.findByText("The terms are saved")).toBeInTheDocument();
    // The answer's derived dates head the section, and a second save sends the new revision.
    const row = fetchMock.state.allocations.find((one) => one.id === 501);
    expect(await within(termsForm()).findByText(/^Earliest release /)).toHaveTextContent(
      `Earliest release ${shownDate(row?.earliestRelease ?? "")} · Handover from ${shownDate(row?.handoverStart ?? "")}`,
    );
    await userEvent.click(within(termsForm()).getByRole("button", { name: "Save terms" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/allocations\/501\/terms$/)).toHaveLength(2));
    expect(sent(fetchMock, "PUT", /\/allocations\/501\/terms$/)[1].ifMatch).toBe("1");
  });

  it("replaces the terms whole at their revision", async () => {
    const fetchMock = stubPlanningApi({ allocations: withTerms() });
    const terms = await openTerms();

    expect(within(terms).getByText(/^Earliest release /)).toHaveTextContent(
      `Earliest release ${shownDate(RELEASE)} · Handover from ${shownDate(HANDOVER)}`,
    );
    expect(within(terms).getByRole("list", { name: "Customers" })).toHaveTextContent("Fjord AS");
    await retype(within(terms).getByRole("textbox", { name: /^Reference/ }), "PO-78");
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));

    await waitFor(() => expect(sent(fetchMock, "PUT", /\/allocations\/501\/terms$/)).toHaveLength(1));
    const [put] = sent(fetchMock, "PUT", /\/allocations\/501\/terms$/);
    expect(put.ifMatch).toBe("3");
    expect(put.body).toEqual({
      noticeCount: 1,
      noticeUnit: "months",
      noticeGivenOn: allocationWithTerms().terms?.noticeGivenOn,
      replacementNeedsApproval: false,
      handoverDays: 10,
      nonCompeteUntil: addDays(TODAY, 365),
      nonCompeteCustomerIds: [7],
      reference: "PO-78",
    });
    expect(await screen.findByText("The terms are saved")).toBeInTheDocument();
  });

  it("clears the terms after asking", async () => {
    const fetchMock = stubPlanningApi({ allocations: withTerms() });
    const terms = await openTerms();

    await userEvent.click(within(terms).getByRole("button", { name: "Clear terms" }));
    const ask = await screen.findByRole("dialog", { name: "Clear the terms?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Clear terms" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/allocations\/501\/terms$/)).toHaveLength(1));
    expect(await screen.findByText("The terms are cleared")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(termsForm()).queryByRole("button", { name: "Clear terms" })).not.toBeInTheDocument(),
    );
    expect(within(termsForm()).queryByText(/^Earliest release /)).not.toBeInTheDocument();
    expect(fetchMock.state.allocations.find((one) => one.id === 501)?.terms).toBeUndefined();
  });

  it("puts the server's field errors on their fields, and checks the halves of a term itself", async () => {
    const fetchMock = stubPlanningApi({
      write: (method, path) =>
        method === "PUT" && path === "/allocations/501/terms"
          ? planningProblem(400, undefined, {
              title: "Invalid allocation",
              errors: { nonCompeteCustomerIds: ["Customer 7 is unknown"], noticeGivenOn: ["After the row's end"] },
            })
          : undefined,
      allocations: withTerms(),
    });
    const terms = await openTerms();

    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));
    expect(await within(terms).findByText("Customer 7 is unknown")).toBeInTheDocument();
    expect(within(terms).getByText("After the row's end")).toBeInTheDocument();

    // A notice period wants both halves; the check is the form's own, so nothing more is sent.
    await choose(terms, "Notice period, unit", "weeks");
    await userEvent.clear(within(terms).getByRole("textbox", { name: "Notice period, count" }));
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));
    expect(await within(terms).findAllByText("Give both the count and the unit, or neither")).not.toHaveLength(0);
    expect(sent(fetchMock, "PUT", /\/allocations\/501\/terms$/)).toHaveLength(1);
  });

  it("reloads and closes the sheet when somebody else changed the terms first", async () => {
    stubPlanningApi({
      allocations: withTerms(),
      write: (method, path) =>
        method === "PUT" && path === "/allocations/501/terms"
          ? planningProblem(409, "revision_conflict", { title: "Revision conflict" })
          : undefined,
    });
    const terms = await openTerms();
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));
    expect(await screen.findByText("Somebody else saved this; it has been reloaded")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Change allocation" })).not.toBeInTheDocument());
  });

  it("keeps the named customers removable when the directory may not be searched", async () => {
    stubPlanningApi({ allocations: withTerms(), customers: "forbidden" });
    const terms = await openTerms();
    // The directory is not searched until the picker is opened.
    expect(within(terms).queryByText(/You may not search the customers/)).not.toBeInTheDocument();
    await userEvent.click(within(terms).getByRole("combobox", { name: "Add a customer" }));

    expect(await within(terms).findByText(/You may not search the customers/)).toBeInTheDocument();
    expect(within(terms).queryByRole("combobox", { name: "Add a customer" })).not.toBeInTheDocument();
    await userEvent.click(within(terms).getByRole("button", { name: "Remove Fjord AS" }));
    expect(within(terms).queryByRole("list", { name: "Customers" })).not.toBeInTheDocument();
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));
    expect(await within(terms).findByText("Name the customers the non-compete is toward")).toBeInTheDocument();
  });

  it("says a closed project's refusal of the terms in its own words", async () => {
    stubPlanningApi({
      allocations: withTerms(),
      write: (method, path) =>
        method === "PUT" && path === "/allocations/501/terms"
          ? planningProblem(409, "project_closed", { title: "Project closed", detail: "The project is closed." })
          : undefined,
    });
    const terms = await openTerms();
    await userEvent.click(within(terms).getByRole("button", { name: "Save terms" }));

    expect(await within(terms).findByText("Could not save")).toBeInTheDocument();
    expect(
      within(terms).getByText("The project is completed or cancelled: it takes no new booking and no change."),
    ).toBeInTheDocument();
  });

  it("names a customer the directory no longer answers by its id", async () => {
    stubPlanningApi({
      allocations: [allocationWithTerms({ nonCompeteCustomers: [{ id: 42 }] }), ...allocations().slice(1)],
    });
    const terms = await openTerms();

    expect(within(terms).getByRole("list", { name: "Customers" })).toHaveTextContent("Customer 42");
    expect(within(terms).getByRole("button", { name: "Remove Customer 42" })).toBeInTheDocument();
  });

  it("says the non-compete the sheet's own save was written under", async () => {
    const until = addDays(TODAY, 200);
    stubPlanningApi({ allocations: withTerms(), warnings: [{ code: "non_compete", allocationId: 777, date: until }] });
    await openTerms();
    const sheet = screen.getByRole("dialog", { name: "Change allocation" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(await within(sheet).findByText("Saved — but look at this")).toBeInTheDocument();
    expect(
      within(sheet).getByText(`The person is under a non-compete toward this customer until ${shownDate(until)}.`),
    ).toBeInTheDocument();
  });

  it("says a confirmed save refused under a non-compete", async () => {
    stubPlanningApi({
      allocations: withTerms(),
      write: (method, path) =>
        method === "PUT" && path === "/allocations/501"
          ? planningProblem(409, "non_compete", { title: "Non-compete" })
          : undefined,
    });
    await openTerms();
    const sheet = screen.getByRole("dialog", { name: "Change allocation" });
    await userEvent.click(within(sheet).getByRole("button", { name: "Save" }));

    expect(
      await within(sheet).findByText("The person is under a non-compete toward this customer."),
    ).toBeInTheDocument();
  });

  it("is not offered on a new allocation", async () => {
    stubPlanningApi();
    renderRoute("/planning/projects/100");
    await userEvent.click(await screen.findByRole("button", { name: "New allocation" }));
    const sheet = await screen.findByRole("dialog", { name: "New allocation" });
    expect(within(sheet).queryByRole("form", { name: "Terms" })).not.toBeInTheDocument();
  });
});

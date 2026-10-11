import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { person } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { type StaffStub, stubStaffApi } from "../test/server";

const peopleTable = () => screen.findByRole("table", { name: "People" });

/** A person's row, found by the link their name is — the name also appears as somebody else's manager. */
const row = async (name: string) =>
  (await within(await peopleTable()).findByRole("link", { name })).closest("tr") as HTMLElement;

/** The query of the latest list read, as the server received it. */
const lastListQuery = (fetchMock: StaffStub): URLSearchParams => {
  const reads = fetchMock.actualCalls.filter(
    ([url]) => String(url).startsWith("/api/v1/staff/people?") || String(url) === "/api/v1/staff/people",
  );
  const [url] = reads[reads.length - 1] ?? [];
  return new URL(String(url), "http://localhost").searchParams;
};

const choose = async (label: string, option: RegExp | string) => {
  await userEvent.click(screen.getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

describe("PeoplePage — HR", () => {
  it("lists the current people with where each stands today, the ended left out", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people");

    expect(await row("Ada Lovelace")).toHaveTextContent("Active");
    expect(await row("Alan Turing")).toHaveTextContent("Starts on Nov 1, 2026");
    expect(await row("Barbara Liskov")).toHaveTextContent("No employment");
    expect(within(await peopleTable()).queryByRole("link", { name: "Edsger Dijkstra" })).not.toBeInTheDocument();
    expect(within(await peopleTable()).getByRole("columnheader", { name: "Status" })).toBeInTheDocument();
  });

  it("shows the manager and the department in force today", async () => {
    stubStaffApi({ role: "manage" });
    renderRoute("/staff/people");

    const grace = await row("Grace Hopper");
    expect(grace).toHaveTextContent("E-2");
    expect(grace).toHaveTextContent("Operations");
    expect(grace).toHaveTextContent("Ada Lovelace");
  });

  it("filters by status into the URL and asks the server for it", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    const { router } = renderRoute("/staff/people");

    await row("Ada Lovelace");
    await userEvent.click(screen.getByRole("radio", { name: "Ended" }));

    expect(await row("Edsger Dijkstra")).toHaveTextContent("Ended on Jun 30, 2026");
    expect(lastListQuery(fetchMock).get("status")).toBe("ended");
    expect(router.state.location.search).toMatchObject({ status: "ended" });
  });

  it("filters by business unit, department and location, every one sent", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people");

    await row("Ada Lovelace");
    await choose("Business unit", "Bergen office");
    await waitFor(() => expect(lastListQuery(fetchMock).get("unitId")).toBe("12"));
    await choose("Department", "Operations (OPS)");
    await waitFor(() => expect(lastListQuery(fetchMock).get("departmentId")).toBe("22"));
    await choose("Location", "Bergen");
    await waitFor(() => expect(lastListQuery(fetchMock).get("locationId")).toBe("32"));

    expect(await row("Grace Hopper")).toBeInTheDocument();
    expect(within(await peopleTable()).queryByRole("link", { name: "Ada Lovelace" })).not.toBeInTheDocument();
  });

  it("searches by the words typed, from the first page", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/people?page=1");

    await row("Ada Lovelace");
    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "hopper");

    await waitFor(() => expect(lastListQuery(fetchMock).get("q")).toBe("hopper"));
    await waitFor(async () =>
      expect(within(await peopleTable()).queryByRole("link", { name: "Ada Lovelace" })).not.toBeInTheDocument(),
    );
    expect(await row("Grace Hopper")).toBeInTheDocument();
  });

  it("pages through a long directory", async () => {
    const many = Array.from({ length: 30 }, (_, index) =>
      person({
        id: 500 + index,
        givenName: "Person",
        familyName: String(index).padStart(2, "0"),
        displayName: `Person ${String(index).padStart(2, "0")}`,
        employeeNumber: `E-${500 + index}`,
      }),
    );
    const fetchMock = stubStaffApi({ role: "manage", people: many });
    renderRoute("/staff/people");

    await row("Person 00");
    expect(within(await peopleTable()).queryByRole("link", { name: "Person 29" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "2" }));

    expect(await row("Person 29")).toBeInTheDocument();
    expect(lastListQuery(fetchMock).get("page")).toBe("2");
  });

  it("opens New person from the URL, once, and offers it as a button", async () => {
    stubStaffApi({ role: "manage" });
    const { router } = renderRoute("/staff/people?create=person");

    const dialog = await screen.findByRole("dialog", { name: "New person" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("create"));
    expect(await screen.findByRole("button", { name: "New person" })).toBeInTheDocument();
  });
});

describe("PeoplePage — a colleague", () => {
  it("lists the people active today without a status column, a status filter or New person", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    renderRoute("/staff/people?status=ended");

    expect(await row("Ada Lovelace")).toBeInTheDocument();
    expect(within(await peopleTable()).queryByRole("link", { name: "Alan Turing" })).not.toBeInTheDocument();
    expect(within(await peopleTable()).queryByRole("columnheader", { name: "Status" })).not.toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "Ended" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New person" })).not.toBeInTheDocument();
    // A status in a pasted link is HR's filter: a colleague's page never sends it.
    expect(lastListQuery(fetchMock).has("status")).toBe(false);
  });

  it("never opens New person from a pasted link", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people?create=person");

    expect(await row("Ada Lovelace")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "New person" })).not.toBeInTheDocument();
  });

  it("links a row to the person's page", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/people");

    await userEvent.click(within(await row("Grace Hopper")).getByRole("link", { name: "Grace Hopper" }));
    expect(await screen.findByTestId("person-page")).toHaveTextContent("102");
  });
});

describe("PeoplePage — HR without the directory", () => {
  it("still lists, with the status and the structure filters", async () => {
    stubStaffApi({ role: "manageOnly" });
    renderRoute("/staff/people");

    expect(await row("Ada Lovelace")).toHaveTextContent("Active");
    expect(screen.getByRole("combobox", { name: "Business unit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New person" })).toBeInTheDocument();
  });
});

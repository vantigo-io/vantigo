import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { validateCompetenceSearch } from "../lib/search";
import { choose, region, retype, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubStaffApi } from "../test/server";

const matrix = () => screen.findByRole("table", { name: "Matrix" });

/** A person's row of the matrix, by the link their name is. */
const row = async (name: string) =>
  (await within(await matrix()).findByRole("link", { name })).closest("tr") as HTMLElement;

/** One cell of a row, by its column's header. */
const cell = async (person: string, competence: string) => {
  const table = await matrix();
  const headers = within(table)
    .getAllByRole("columnheader")
    .map((one) => one.textContent);
  const index = headers.indexOf(competence);
  expect(index, `a column ${competence}`).toBeGreaterThan(0);
  return within(await row(person)).getAllByRole("cell")[index];
};

describe("The Competence area — the matrix", () => {
  it("lists every person active today with what they hold valid today, the declared marked", async () => {
    stubStaffApi({ role: "view" });
    renderRoute("/staff/competence");

    expect(await row("Ada Lovelace")).toHaveAttribute("data-person", "101");
    expect(within(await row("Ada Lovelace")).getByRole("link", { name: "Ada Lovelace" })).toHaveAttribute(
      "href",
      "/staff/people/101?tab=competence",
    );
    // Neither the upcoming nor the ended person is listed.
    expect(within(await matrix()).queryByRole("link", { name: "Alan Turing" })).not.toBeInTheDocument();
    expect(within(await matrix()).queryByRole("link", { name: "Edsger Dijkstra" })).not.toBeInTheDocument();

    const welding = await cell("Ada Lovelace", "Welding");
    expect(welding).toHaveTextContent("independent");
    expect(within(welding).getByText("Declared")).toBeInTheDocument();
    const fse = await cell("Ada Lovelace", "Electrical safety (FSE)");
    expect(within(fse).getByLabelText("Held")).toBeInTheDocument();
    expect(fse).toHaveTextContent("Valid to Nov 1, 2026");
    expect(fse).not.toHaveTextContent("Declared");
    expect(await cell("Grace Hopper", "English")).toHaveTextContent("experienced");
    // A colleague is offered no interests overview.
    expect(screen.queryByRole("tab", { name: "Interests" })).not.toBeInTheDocument();
  });

  it("keeps the verified alone when asked, in the URL", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    const { router } = renderRoute("/staff/competence");

    await row("Ada Lovelace");
    await userEvent.click(screen.getByRole("switch", { name: /^Verified only/ }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ verifiedOnly: true }));
    await waitFor(() =>
      expect(within(screen.getByRole("table", { name: "Matrix" })).queryByText("Declared")).not.toBeInTheDocument(),
    );
    expect(sent(fetchMock, "GET", /\/competence-matrix\?verifiedOnly=true$/).length).toBeGreaterThan(0);
    // Everyone active is listed still: Grace holds nothing verified.
    expect(await row("Grace Hopper")).toBeInTheDocument();
  });

  it("narrows to the people holding every competence chosen, at the level chosen", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    const { router } = renderRoute("/staff/competence");

    await row("Ada Lovelace");
    await choose(document.body, "Competences", "Welding");
    await waitFor(() =>
      expect(
        within(screen.getByRole("table", { name: "Matrix" })).queryByRole("link", { name: "Grace Hopper" }),
      ).not.toBeInTheDocument(),
    );
    expect(await row("Ada Lovelace")).toBeInTheDocument();
    expect(router.state.location.search).toMatchObject({ competenceId: [2004] });
    expect(sent(fetchMock, "GET", /\/competence-matrix\?competenceId=2004$/).length).toBeGreaterThan(0);

    await choose(document.body, "Minimum level", "experienced");
    expect(await screen.findByText("Nobody meets the filter")).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/competence-matrix\?competenceId=2004&minLevel=3$/).length).toBeGreaterThan(0);
  });

  it("reads the matrix on another day: a certificate expired by then is not held", async () => {
    const fetchMock = stubStaffApi({ role: "view" });
    const { router } = renderRoute("/staff/competence");

    await cell("Ada Lovelace", "Electrical safety (FSE)");
    await retype(screen.getByRole("textbox", { name: "Valid on" }), "Dec 1, 2026");
    await userEvent.tab();

    await waitFor(() => expect(router.state.location.search).toMatchObject({ validOn: "2026-12-01" }));
    await waitFor(() =>
      expect(
        within(screen.getByRole("table", { name: "Matrix" })).queryByRole("columnheader", {
          name: "Electrical safety (FSE)",
        }),
      ).not.toBeInTheDocument(),
    );
    expect(sent(fetchMock, "GET", /\/competence-matrix\?validOn=2026-12-01$/).length).toBeGreaterThan(0);
    expect(await cell("Ada Lovelace", "Welding")).toHaveTextContent("independent");
  });

  it("opens with the filters a link carries", async () => {
    const fetchMock = stubStaffApi({ role: "manage" });
    renderRoute("/staff/competence?categoryId=1003&verifiedOnly=true");

    expect(await screen.findByRole("switch", { name: /^Verified only/ })).toBeChecked();
    await waitFor(() =>
      expect(sent(fetchMock, "GET", /\/competence-matrix\?categoryId=1003&verifiedOnly=true$/).length).toBeGreaterThan(
        0,
      ),
    );
  });
});

describe("The Competence area — the interests", () => {
  it("shows HR who wants to learn what and who wants to use more of it", async () => {
    stubStaffApi({ role: "manage" });
    const { router } = renderRoute("/staff/competence");

    await userEvent.click(await screen.findByRole("tab", { name: "Interests" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ tab: "interests" }));

    const welding = await region("Welding");
    expect(
      within(within(welding).getByRole("list", { name: "Wants to use more" })).getByRole("link", {
        name: "Ada Lovelace",
      }),
    ).toHaveAttribute("href", "/staff/people/101?tab=competence");
    expect(
      within(within(welding).getByRole("list", { name: "Wants to learn" })).getByRole("link", { name: "Grace Hopper" }),
    ).toBeInTheDocument();
    const english = await region("English");
    expect(
      within(within(english).getByRole("list", { name: "Wants to learn" })).getByRole("link", { name: "Ada Lovelace" }),
    ).toBeInTheDocument();
    expect(within(english).queryByRole("list", { name: "Wants to use more" })).not.toBeInTheDocument();
  });

  // The overview is staff:manage's alone (plan reading 30): anyone else asking for it is shown the matrix.
  it.each(["view", "approve"] as const)(
    "opens the matrix for %s asking for the interests, and never reads them",
    async (role) => {
      const fetchMock = stubStaffApi({ role });
      renderRoute("/staff/competence?tab=interests");

      expect(await row("Ada Lovelace")).toBeInTheDocument();
      expect(screen.queryByRole("tab", { name: "Interests" })).not.toBeInTheDocument();
      expect(sent(fetchMock, "GET", /\/competence-interests/)).toHaveLength(0);
    },
  );

  it("says so when nobody has written an interest", async () => {
    stubStaffApi({ role: "manage", interests: [] });
    renderRoute("/staff/competence?tab=interests");

    expect(await screen.findByText("Nobody has written an interest yet")).toBeInTheDocument();
  });
});

describe("validateCompetenceSearch", () => {
  it("keeps the filters a link may carry and leaves out what it invents", () => {
    expect(
      validateCompetenceSearch({
        tab: "interests",
        categoryId: "1002",
        competenceId: [2004, "2005", "x"],
        minLevel: "3",
        validOn: "2026-12-01",
        verifiedOnly: "true",
      }),
    ).toEqual({
      tab: "interests",
      categoryId: 1002,
      competenceId: [2004, 2005],
      minLevel: 3,
      validOn: "2026-12-01",
      verifiedOnly: true,
    });
    expect(validateCompetenceSearch({ competenceId: "2004,2001" })).toEqual({ competenceId: [2004, 2001] });
    expect(
      validateCompetenceSearch({ tab: "cv", categoryId: 0, minLevel: 5, validOn: "1 Dec", verifiedOnly: "yes" }),
    ).toEqual({});
  });
});

import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { wish, wishes } from "../test/fixtures";
import { sent, shownDate, shownStamp } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { planningProblem, stubPlanningApi } from "../test/server";

const rowOf = async (name: string) =>
  (await within(await screen.findByRole("table", { name: "Wishes" })).findByText(name)).closest("tr") as HTMLElement;

describe("WishesPage — the planners' open wishes", () => {
  it("lists the open wishes oldest first, with the person, the kind, the project, the days and the seen stamp", async () => {
    const [ada, linus] = wishes();
    stubPlanningApi();
    const { router } = renderRoute("/planning/wishes");

    expect(await screen.findByRole("heading", { name: "Wishes" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Wishes" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((row) => within(row).getAllByRole("cell")[0].textContent)).toEqual([
      "Ada Lovelace",
      "Linus Torvalds",
    ]);
    const [first, second] = rows.map((row) => within(row).getAllByRole("cell"));
    expect(first.slice(1, 6).map((cell) => cell.textContent)).toEqual([
      "Off",
      "P-100 Harbour app",
      shownDate(ada.fromDate),
      shownStamp(ada.createdAt),
      "New",
    ]);
    expect(second.slice(1, 6).map((cell) => cell.textContent)).toEqual([
      "Something new",
      "—",
      shownDate(linus.fromDate),
      shownStamp(linus.createdAt),
      `Seen ${shownStamp(linus.seenAt ?? "")}`,
    ]);
    // A seen wish is not offered to be marked again.
    expect(within(rows[1]).queryByRole("button", { name: /^Mark seen/ })).not.toBeInTheDocument();

    await userEvent.click(within(rows[0]).getByRole("link", { name: "P-100 Harbour app" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/planning/projects/100"));
  });

  it("links the person to their timeline, and names an erased person without a link", async () => {
    stubPlanningApi({ wishes: [wish({ id: 901, personId: 1 }), wish({ id: 903, personId: 99 })] });
    const { router } = renderRoute("/planning/wishes");

    const erased = await rowOf("Erased person");
    expect(within(erased).queryByRole("link")).not.toBeInTheDocument();
    await userEvent.click(within(await rowOf("Ada Lovelace")).getByRole("link", { name: "Ada Lovelace" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/planning/people/1"));
  });

  it("marks a wish seen", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/wishes");

    await userEvent.click(
      within(await rowOf("Ada Lovelace")).getByRole("button", { name: /^Mark seen: Ada Lovelace/ }),
    );
    await waitFor(() => expect(sent(fetchMock, "POST", /\/wishes\/901\/seen$/)).toHaveLength(1));
    expect(await screen.findByText("The wish is marked seen")).toBeInTheDocument();
    await waitFor(async () => expect(await rowOf("Ada Lovelace")).toHaveTextContent(/Seen /));
    expect(within(await rowOf("Ada Lovelace")).queryByRole("button", { name: /^Mark seen/ })).not.toBeInTheDocument();
  });

  it("resolves a wish after asking", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/wishes");

    await userEvent.click(
      within(await rowOf("Linus Torvalds")).getByRole("button", { name: /^Resolve: Linus Torvalds/ }),
    );
    const ask = await screen.findByRole("dialog", { name: "Resolve the wish?" });
    expect(ask).toHaveTextContent("It is deleted and the record keeps the kind, the project and the date.");
    await userEvent.click(within(ask).getByRole("button", { name: "Resolve" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/wishes\/902\/resolve$/)).toHaveLength(1));
    expect(await screen.findByText("The wish is resolved")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Linus Torvalds")).not.toBeInTheDocument());
  });

  it("says a refused mark in a notification and reads the list again", async () => {
    const fetchMock = stubPlanningApi({
      write: (method, path) =>
        method === "POST" && path === "/wishes/901/seen"
          ? planningProblem(409, undefined, { title: "Refused", detail: "The wish cannot be stamped now." })
          : undefined,
    });
    renderRoute("/planning/wishes");
    await userEvent.click(
      within(await rowOf("Ada Lovelace")).getByRole("button", { name: /^Mark seen: Ada Lovelace/ }),
    );

    expect(await screen.findByText("Could not save")).toBeInTheDocument();
    expect(screen.getByText("The wish cannot be stamped now.")).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/planning\/wishes$/).length).toBeGreaterThan(1));
  });

  it("says a wish withdrawn or resolved meanwhile is gone, and reads the list again", async () => {
    const fetchMock = stubPlanningApi();
    renderRoute("/planning/wishes");
    await userEvent.click(
      within(await rowOf("Linus Torvalds")).getByRole("button", { name: /^Resolve: Linus Torvalds/ }),
    );
    const ask = await screen.findByRole("dialog", { name: "Resolve the wish?" });
    // Linus withdraws it while the planner reads the question.
    fetchMock.state.wishes = fetchMock.state.wishes.filter((one) => one.id !== 902);
    await userEvent.click(within(ask).getByRole("button", { name: "Resolve" }));

    expect(await screen.findByText("Could not save")).toBeInTheDocument();
    expect(screen.getByText("The wish is gone: it was withdrawn or resolved meanwhile.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Linus Torvalds")).not.toBeInTheDocument());
  });

  it("says why the people are not named when Staff shows the planner nobody, and keeps the list", async () => {
    stubPlanningApi({
      meta: { workforceVisible: false },
      wishes: [wish({ id: 903, personId: 99, projectId: 100 })],
    });
    renderRoute("/planning/wishes");

    expect(await screen.findByText("Staff shows you nobody")).toBeInTheDocument();
    expect(screen.getByText(/until then the plan is not empty, only hidden/)).toBeInTheDocument();
    const row = await rowOf("A colleague");
    expect(within(row).queryByRole("link", { name: "A colleague" })).not.toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "P-100 Harbour app" })).toBeInTheDocument();
  });

  it("says when there is no open wish", async () => {
    stubPlanningApi({ wishes: [] });
    renderRoute("/planning/wishes");
    expect(await screen.findByText("No open wishes")).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "Wishes" })).not.toBeInTheDocument();
  });
});

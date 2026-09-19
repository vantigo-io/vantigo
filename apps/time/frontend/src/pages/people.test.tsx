import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse } from "../test/api";
import { peopleOverview } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";
import { windowOf } from "./people";

/** The `weeks` every read of the overview asked for, in order. */
const weeksAsked = (calls: [RequestInfo | URL, RequestInit | undefined][]): (string | null)[] =>
  calls
    .map(([input]) => new URL(String(input), "http://localhost"))
    .filter((url) => url.pathname === "/api/v1/time/people")
    .map((url) => url.searchParams.get("weeks"));

describe("PeoplePage", () => {
  it("shows a row per person, a column per week, and each week's hours and state", async () => {
    const fetchMock = stubTimeApi({ people: peopleOverview });
    renderRoute("/time/people");

    const ada = (await screen.findByText("Ada Lovelace")).closest("tr") as HTMLElement;
    expect(ada).toHaveTextContent("32");
    expect(ada).toHaveTextContent("32 h approved");
    expect(ada).toHaveTextContent("1 rejected");
    expect(within(ada).getByText("Submitted")).toBeInTheDocument();

    const grace = screen.getByText("Grace Hopper").closest("tr") as HTMLElement;
    expect(grace).toHaveTextContent("4");

    expect(weeksAsked(fetchMock.actualCalls)).toEqual(["4"]);
  });

  it("reads the window from the URL and puts a new one back into it", async () => {
    const fetchMock = stubTimeApi({ people: peopleOverview });
    const { router } = renderRoute("/time/people?weeks=12");

    await screen.findByText("Ada Lovelace");
    expect(weeksAsked(fetchMock.actualCalls)).toEqual(["12"]);

    await userEvent.click(screen.getByRole("combobox", { name: "Weeks" }));
    await userEvent.click(await screen.findByRole("option", { name: "8 weeks" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ weeks: 8 }));
    await waitFor(() => expect(weeksAsked(fetchMock.actualCalls)).toContain("8"));
  });

  it("says so plainly when the caller may not see everyone's time", async () => {
    stubTimeApi({ people: problemResponse(403, "Forbidden") });
    renderRoute("/time/people");

    expect(await screen.findByText("You cannot see everyone's time")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    // Nothing to narrow when there is nothing to see.
    expect(screen.queryByRole("combobox", { name: "Weeks" })).not.toBeInTheDocument();
  });
});

/**
 * `weeks` out of range is the host route's problem — it drops the value to
 * `undefined` before the page ever sees it (`apps/host/frontend/src/routes/time/people.tsx`,
 * mirrored in `test/route-tree.tsx`'s `asWeekWindow`). The page has only its
 * own default to supply, not a second clamp that could disagree with the
 * route's.
 */
describe("windowOf", () => {
  it("falls back to the default only when there is no window at all", () => {
    expect(windowOf(undefined)).toBe(4);
    expect(windowOf(1)).toBe(1);
    expect(windowOf(8)).toBe(8);
    expect(windowOf(12)).toBe(12);
  });

  // Out of range is never supposed to reach the page — the route drops it —
  // so the page has nothing of its own to clamp it to; a second opinion here
  // would only give a value that disagrees with the route's `undefined`.
  it("does not clamp a value the route was supposed to have dropped", () => {
    expect(windowOf(13)).toBe(13);
  });
});

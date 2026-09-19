import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse } from "../test/api";
import { peopleOverview } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubTimeApi } from "../test/server";

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
  });
});

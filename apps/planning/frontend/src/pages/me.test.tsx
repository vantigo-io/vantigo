import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { addDays } from "../lib/weeks";
import { allocation, TODAY, weekEnd, wish, wishes } from "../test/fixtures";
import { choose, sent, shownDate, shownStamp } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { planningProblem, stubPlanningApi } from "../test/server";

describe("MyPlanningPage — on a phone", () => {
  // A phone is narrower than Mantine's `sm` (48em): every max-width query at
  // or above it matches, and nothing wider does.
  const phone = (query: string) => {
    const width = /max-width:\s*([\d.]+)em/.exec(query);
    return width !== null && Number(width[1]) >= 23.4375;
  };
  const original = window.matchMedia;

  afterEach(() => {
    window.matchMedia = original;
    window.innerWidth = 1024;
  });

  const narrow = () => {
    window.innerWidth = 375;
    window.matchMedia = vi.fn((query: string) => ({
      matches: phone(query),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  };

  it("lists my confirmed bookings as cards, never a tentative one", async () => {
    narrow();
    stubPlanningApi({
      role: "access",
      allocations: [
        allocation({ id: 501, personId: 1, projectId: 100, title: "Frontend developer", note: "Planners only" }),
        allocation({ id: 502, personId: 1, projectId: 200, status: "tentative" }),
        allocation({ id: 503, personId: 2, projectId: 100 }),
        allocation({ id: 504, personId: 1, projectId: 100, load: 16, loadUnit: "hours_per_week", toDate: weekEnd(2) }),
      ],
    });
    renderRoute("/planning/me");

    const cards = within(await screen.findByRole("list", { name: "Allocations" })).getAllByRole("listitem");
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveTextContent("P-100 Harbour app");
    expect(cards[0]).toHaveTextContent("Frontend developer");
    expect(cards[0]).toHaveTextContent("50%");
    expect(within(cards[1]).getByText("16 h/week")).toBeInTheDocument();
    expect(screen.queryByText("P-200 Tender bid")).not.toBeInTheDocument();
    expect(screen.queryByText("Planners only")).not.toBeInTheDocument();
  });

  it("says why there is nothing when Staff links me to nobody", async () => {
    narrow();
    stubPlanningApi({ role: "access", me: null });
    renderRoute("/planning/me");
    expect(await screen.findByText("No person is linked to you")).toBeInTheDocument();
    expect(screen.getByText(/Ask HR to link your user to your record/)).toBeInTheDocument();
  });

  it("says when nothing is booked ahead", async () => {
    stubPlanningApi({ role: "access", allocations: [] });
    renderRoute("/planning/me");
    expect(await screen.findByText("Nothing booked ahead")).toBeInTheDocument();
  });

  it("lists my open wishes with whether a planner has seen them", async () => {
    narrow();
    const [ada] = wishes();
    stubPlanningApi({
      role: "access",
      wishes: [ada, { ...ada, id: 905, kind: "new", project: undefined, seenAt: "2026-10-23T10:00:00Z" }],
    });
    renderRoute("/planning/me");

    const mine = await screen.findByRole("list", { name: "My wishes" });
    const [off, fresh] = within(mine).getAllByRole("listitem");
    expect(off).toHaveTextContent(`Off P-100 Harbour app from ${shownDate(ada.fromDate)}`);
    expect(off).toHaveTextContent("Not seen yet");
    expect(fresh).toHaveTextContent(`Something new from ${shownDate(ada.fromDate)}`);
    expect(fresh).toHaveTextContent(`Seen by a planner ${shownStamp("2026-10-23T10:00:00Z")}`);
    expect(screen.getByText(/no free text is asked, only a project and a date/)).toBeInTheDocument();
  });

  it("files a wish off a project I am booked on", async () => {
    narrow();
    const fetchMock = stubPlanningApi({ role: "access", wishes: [] });
    renderRoute("/planning/me");
    expect(await screen.findByText("No open wishes")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "I wish off this project from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish off a project" });
    await choose(dialog, "Project", "P-100 Harbour app");
    const from = addDays(TODAY, 21);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), from);
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/wishes$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/wishes$/)[0].body).toEqual({ kind: "off", projectId: 100, fromDate: from });
    expect(await screen.findByText("Your wish is sent to the planners")).toBeInTheDocument();
    const mine = await screen.findByRole("list", { name: "My wishes" });
    expect(within(mine).getByRole("listitem")).toHaveTextContent(`Off P-100 Harbour app from ${shownDate(from)}`);
  });

  it("files a wish for something new, with no booking at all", async () => {
    narrow();
    const fetchMock = stubPlanningApi({ role: "access", allocations: [], wishes: [] });
    renderRoute("/planning/me");
    expect(await screen.findByText("Nothing booked ahead")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "I wish off this project from…" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "I wish for something new from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish for something new" });
    expect(within(dialog).queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();
    const from = addDays(TODAY, 7);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), from);
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/wishes$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/wishes$/)[0].body).toEqual({ kind: "new", fromDate: from });
    expect(await screen.findByText("Your wish is sent to the planners")).toBeInTheDocument();
  });

  it("says the rule a wish is refused under, in the dialog", async () => {
    narrow();
    stubPlanningApi({
      role: "access",
      write: (method, path) =>
        method === "POST" && path === "/me/wishes"
          ? planningProblem(409, "wish_limit", { title: "Too many wishes" })
          : undefined,
    });
    renderRoute("/planning/me");
    await userEvent.click(await screen.findByRole("button", { name: "I wish for something new from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish for something new" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), addDays(TODAY, 7));
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await within(dialog).findByText("Could not send the wish")).toBeInTheDocument();
    expect(within(dialog).getByText(/You already have five open wishes/)).toBeInTheDocument();
  });

  it("asks for the project and the day before sending", async () => {
    narrow();
    const fetchMock = stubPlanningApi({
      role: "access",
      allocations: [
        allocation({ id: 501, personId: 1, projectId: 100 }),
        allocation({ id: 504, personId: 1, projectId: 200 }),
      ],
      wishes: [],
    });
    renderRoute("/planning/me");
    await userEvent.click(await screen.findByRole("button", { name: "I wish off this project from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish off a project" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
    expect(await within(dialog).findByText("Pick a project")).toBeInTheDocument();
    expect(within(dialog).getByText("Pick the day")).toBeInTheDocument();
    expect(sent(fetchMock, "POST", /\/me\/wishes$/)).toHaveLength(0);
  });

  it("withdraws a wish after asking", async () => {
    narrow();
    const [ada] = wishes();
    const fetchMock = stubPlanningApi({ role: "access" });
    renderRoute("/planning/me");

    await userEvent.click(
      await screen.findByRole("button", {
        name: `Withdraw the wish Off P-100 Harbour app from ${shownDate(ada.fromDate)}`,
      }),
    );
    const ask = await screen.findByRole("dialog", { name: "Withdraw this wish?" });
    await userEvent.click(within(ask).getByRole("button", { name: "Withdraw" }));

    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/me\/wishes\/901$/)).toHaveLength(1));
    expect(await screen.findByText("The wish is withdrawn")).toBeInTheDocument();
    expect(await screen.findByText("No open wishes")).toBeInTheDocument();
  });
  it("preselects the one project I may wish off, and leaves out one I already wish off", async () => {
    narrow();
    const fetchMock = stubPlanningApi({
      role: "access",
      allocations: [
        allocation({ id: 501, personId: 1, projectId: 100 }),
        allocation({ id: 504, personId: 1, projectId: 200 }),
      ],
    });
    renderRoute("/planning/me");
    await userEvent.click(await screen.findByRole("button", { name: "I wish off this project from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish off a project" });
    // Ada already wishes off the harbour app: the tender bid is the one left, chosen for her.
    expect(within(dialog).getByRole("combobox", { name: "Project" })).toHaveValue("P-200 Tender bid");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Project" }));
    expect(screen.queryByRole("option", { name: "P-100 Harbour app" })).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    const from = addDays(TODAY, 21);
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), from);
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/me\/wishes$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/me\/wishes$/)[0].body).toEqual({ kind: "off", projectId: 200, fromDate: from });
  });

  it.each([
    ["wish_no_booking", "You have no confirmed booking on that project."],
    ["wish_exists", "You already wish off that project."],
  ])("says a wish refused with %s in its own words", async (code, words) => {
    narrow();
    stubPlanningApi({
      role: "access",
      wishes: [],
      write: (method, path) =>
        method === "POST" && path === "/me/wishes" ? planningProblem(409, code, { title: "Refused" }) : undefined,
    });
    renderRoute("/planning/me");
    await userEvent.click(await screen.findByRole("button", { name: "I wish off this project from…" }));
    const dialog = await screen.findByRole("dialog", { name: "I wish off a project" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "From" }), addDays(TODAY, 7));
    await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

    expect(await within(dialog).findByText("Could not send the wish")).toBeInTheDocument();
    expect(within(dialog).getByText(words)).toBeInTheDocument();
  });

  it("offers no new wish at five open, and says why", async () => {
    narrow();
    stubPlanningApi({
      role: "access",
      wishes: [901, 902, 903, 904, 905].map((id) => wish({ id, personId: 1, fromDate: addDays(TODAY, id - 890) })),
    });
    renderRoute("/planning/me");

    expect(await screen.findByRole("button", { name: "I wish off this project from…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "I wish for something new from…" })).toBeDisabled();
    expect(
      screen.getByText("You already have five open wishes. Withdraw one before you send another."),
    ).toBeInTheDocument();
    expect(screen.queryByText("You have no confirmed booking to wish off.")).not.toBeInTheDocument();
  });

  it("says why I cannot wish off a project when I have no confirmed booking", async () => {
    narrow();
    stubPlanningApi({ role: "access", allocations: [], wishes: [] });
    renderRoute("/planning/me");

    expect(await screen.findByRole("button", { name: "I wish off this project from…" })).toBeDisabled();
    expect(screen.getByText("You have no confirmed booking to wish off.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "I wish for something new from…" })).toBeEnabled();
  });

  it("says a withdrawal refused because the wish is gone, and reads the page again", async () => {
    narrow();
    const [ada] = wishes();
    const fetchMock = stubPlanningApi({ role: "access" });
    renderRoute("/planning/me");
    await userEvent.click(
      await screen.findByRole("button", {
        name: `Withdraw the wish Off P-100 Harbour app from ${shownDate(ada.fromDate)}`,
      }),
    );
    const ask = await screen.findByRole("dialog", { name: "Withdraw this wish?" });
    // A planner resolves it meanwhile.
    fetchMock.state.wishes = fetchMock.state.wishes.filter((one) => one.id !== 901);
    await userEvent.click(within(ask).getByRole("button", { name: "Withdraw" }));

    expect(await screen.findByText("Could not save")).toBeInTheDocument();
    expect(screen.getByText("The wish is gone: it was withdrawn or resolved meanwhile.")).toBeInTheDocument();
    expect(await screen.findByText("No open wishes")).toBeInTheDocument();
  });
});

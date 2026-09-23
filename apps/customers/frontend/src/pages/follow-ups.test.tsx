import { MantineProvider } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { FollowUpsPage } from "./follow-ups";

// vi.hoisted, because vi.mock's factory is hoisted above every import and a
// plain `const` declared here would not exist when it runs — the pattern
// `-customers.index.test.tsx` already uses for the same router. The whole module
// is replaced rather than spread over the real one: the real `Link` needs a
// router context this test has no reason to build.
const router = vi.hoisted(() => ({
  search: { page: 1, assignee: "me", state: "open" } as Record<string, unknown>,
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => router.search,
  useNavigate: () => router.navigate,
  Link: ({ children }: { children: ReactNode }) => <a href="#stub">{children}</a>,
}));

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Literally the wire body: followUp is required on a row, assignee and doneAt
// are omitted rather than null when they have no value.
const page = (rows: unknown[], pagination: Record<string, unknown> = {}) => ({
  data: rows,
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: rows.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
    ...pagination,
  },
});
const row = (overrides: Record<string, unknown> = {}) => ({
  entryId: 7,
  customerId: 42,
  customerName: "Alpha Co",
  eventType: "note",
  occurredOn: "2020-07-20",
  note: "Ring back about the renewal",
  followUp: { dueOn: "2020-08-01" },
  ...overrides,
});

/**
 * What the tick answers, which is NOT a row of this list: POST
 * .../follow-up/done returns the whole timeline ENTRY (`TimelineResponse`), and
 * `markFollowUpDone` runs it through the entry normaliser. A `FollowUpRow` here
 * would type-check nowhere and hide the mismatch behind a response nothing on
 * the page reads.
 */
const tickedEntry = () => ({
  id: 7,
  provenance: "manual",
  eventType: "note",
  producer: "",
  occurredOn: "2020-07-20",
  note: "Ring back about the renewal",
  currentRevision: 3,
  state: "active",
  actorKind: "user",
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  followUp: { dueOn: "2020-08-01", doneAt: "2026-07-22T09:30:00Z" },
});

const renderPage = (fetchMock: ReturnType<typeof vi.fn>, canManageTimeline = true) => {
  const stub = stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <FollowUpsPage canManageTimeline={canManageTimeline} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return stub;
};

/**
 * Every read of the list, matched by method and URL — never "the last fetch".
 * A GET carries no `method` at all (the api client only sets one for unsafe
 * requests), which is also what keeps the tick's POST to the same base path out
 * of this count.
 */
const listReads = (stub: ReturnType<typeof stubFetch>) =>
  stub.actualCalls.filter(
    ([url, init]) => String(url).includes("/api/v1/customers/follow-ups") && (!init?.method || init.method === "GET"),
  );

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  router.search = { page: 1, assignee: "me", state: "open" };
});

describe("the Follow-ups page", () => {
  it("asks for the caller's open follow-ups and shows a row per follow-up", async () => {
    const stub = renderPage(vi.fn(() => Promise.resolve(json(page([row()])))));

    expect(await screen.findByText("Alpha Co")).toBeInTheDocument();
    expect(screen.getByText(/Ring back about the renewal/)).toBeInTheDocument();
    // Never "the last fetch": find the call by URL.
    const call = stub.actualCalls.find(([url]) => String(url).includes("/api/v1/customers/follow-ups"));
    expect(call).toBeDefined();
    const url = new URL(String(call?.[0]), "http://test");
    expect(url.searchParams.get("assignee")).toBe("me");
    expect(url.searchParams.get("state")).toBe("open");
  });

  it("puts a filter change in the URL rather than in its own state", async () => {
    renderPage(vi.fn(() => Promise.resolve(json(page([])))));
    await screen.findByRole("combobox", { name: /state/i });

    await userEvent.click(screen.getByRole("combobox", { name: /state/i }));
    await userEvent.click(await screen.findByRole("option", { name: /overdue/i }));

    await waitFor(() => {
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ state: "overdue", page: 1 }) }),
      );
    });
  });

  it("reads the filters back out of the URL when the URL is what changed", async () => {
    router.search = { page: 1, assignee: "none", state: "done" };
    const stub = renderPage(vi.fn(() => Promise.resolve(json(page([])))));

    await waitFor(() => {
      const call = stub.actualCalls.find(([url]) => String(url).includes("/api/v1/customers/follow-ups"));
      const url = new URL(String(call?.[0]), "http://test");
      expect(url.searchParams.get("assignee")).toBe("none");
      expect(url.searchParams.get("state")).toBe("done");
    });
  });

  it("says so when there is nothing to follow up", async () => {
    renderPage(vi.fn(() => Promise.resolve(json(page([])))));
    expect(await screen.findByText("No follow-ups here.")).toBeInTheDocument();
  });

  it("ticks a row done through the entry's own path and re-reads the list", async () => {
    const stub = renderPage(
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/follow-up/done")) return Promise.resolve(json(tickedEntry()));
        return Promise.resolve(json(page([row()])));
      }),
    );
    await userEvent.click(await screen.findByRole("button", { name: /mark done/i }));
    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) =>
          String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "POST",
      );
      expect(call).toBeDefined();
    });
    // A ticked row leaves the default "open" list, so the read after the write
    // is what takes it off screen.
    await waitFor(() => expect(listReads(stub).length).toBeGreaterThanOrEqual(2));
  });

  // The card's own rule (`-customer-timeline.tsx`: the done mutation refreshes
  // on failure too): the entry may be gone, and a row that stays on screen with
  // a live button invites the same 404 again. So the failure path re-reads
  // BEFORE it says anything.
  it("re-reads the list when the tick finds the entry gone, and says so", async () => {
    const stub = renderPage(
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/follow-up/done")) return Promise.resolve(json({ title: "Not found" }, 404));
        return Promise.resolve(json(page([row()])));
      }),
    );
    await userEvent.click(await screen.findByRole("button", { name: /mark done/i }));
    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Could not update the follow-up" }),
      ),
    );
    // In a waitFor of its own: the notification and the refetch are two effects
    // of one rejection, and nothing orders them — on a slow runner the message
    // can land first and a bare assertion would see only the original read.
    await waitFor(() => expect(listReads(stub).length).toBeGreaterThanOrEqual(2));
  });

  it("offers the pages when there is more than one, and page 2 reaches the URL and the wire", async () => {
    // Rendered here rather than through `renderPage`, because this is the one
    // case that needs a SECOND render of the same tree: the page number lives in
    // the URL, so what the router does after `navigate` — hand the component a
    // new search — is half of what is under test, and `rerender` is how the
    // stubbed router's next value gets read.
    const stub = stubFetch(
      vi.fn(() => Promise.resolve(json(page([row()], { totalPages: 2, totalCount: 30, hasNextPage: true })))),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    // A fresh element each time, not one stored in a const: React bails out of
    // re-rendering a tree handed back the very same element object, and the
    // second read would never happen.
    const tree = () => (
      <MantineProvider env="test">
        <QueryClientProvider client={queryClient}>
          <FollowUpsPage canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>
    );
    const { rerender } = render(tree());

    await userEvent.click(await screen.findByRole("button", { name: "2" }));
    // Not `go()`'s reset-to-1: a page change is the one navigation that must
    // keep the number it was given.
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ page: 2 }) }),
    );

    router.search = { ...router.search, page: 2 };
    rerender(tree());
    await waitFor(() => {
      const secondPage = listReads(stub).find(
        ([url]) => new URL(String(url), "http://test").searchParams.get("page") === "2",
      );
      expect(secondPage).toBeDefined();
    });
  });

  // Its own `it`, not a second render inside the one above: two renders in one
  // test leave two copies of the table in the document, and `queryByRole` then
  // finds the first render's button and the assertion passes for the wrong
  // reason. `afterEach`'s cleanup() is what makes one render per test true.
  it("hides the tick from a reader who cannot manage the timeline", async () => {
    renderPage(
      vi.fn(() => Promise.resolve(json(page([row()])))),
      false,
    );
    expect(await screen.findByText("Alpha Co")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /mark done/i })).not.toBeInTheDocument();
    // And the column they head is gone with them: a header over nothing reads
    // as a column that failed to load.
    expect(screen.queryByRole("columnheader", { name: /actions/i })).not.toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: /assigned to/i })).toBeInTheDocument();
  });

  it("marks an assignee the directory no longer has as inactive", async () => {
    // The disabled case the server deliberately keeps: somebody given a
    // follow-up and since removed from the directory still holds it, so the name
    // stays and says why it is odd.
    renderPage(
      vi.fn(() =>
        Promise.resolve(
          json(
            page([
              row({
                followUp: {
                  dueOn: "2020-08-01",
                  assignee: { userId: "u1", displayName: "Kari Nordmann", active: false },
                },
              }),
            ]),
          ),
        ),
      ),
    );
    expect(await screen.findByText(/Kari Nordmann \(inactive\)/)).toBeInTheDocument();
  });

  // A caller the nav admits with `customers:timeline-view` alone reaches this
  // page and the endpoint answers 403, because it also wants `customers:view`
  // for the customer names. "Could not load follow-ups" on its own would read as
  // a glitch worth reloading for; the server's own sentence is what says it is
  // not.
  it("says why the list did not load, and reads again on Retry", async () => {
    let attempt = 0;
    const stub = renderPage(
      vi.fn(() => {
        attempt += 1;
        return attempt === 1
          ? Promise.resolve(json({ title: "Forbidden", detail: "You do not have permission to view customers" }, 403))
          : Promise.resolve(json(page([row()])));
      }),
    );

    expect(await screen.findByText(/permission to view customers/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));

    expect(await screen.findByText("Alpha Co")).toBeInTheDocument();
    await waitFor(() => expect(listReads(stub).length).toBeGreaterThanOrEqual(2));
  });
});

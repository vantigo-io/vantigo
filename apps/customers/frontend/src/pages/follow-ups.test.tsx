import { MantineProvider } from "@mantine/core";
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

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Literally the wire body: followUp is required on a row, assignee and doneAt
// are omitted rather than null when they have no value.
const page = (rows: unknown[]) => ({
  data: rows,
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: rows.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
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

  it("ticks a row done through the entry's own path", async () => {
    const stub = renderPage(
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/follow-up/done")) return Promise.resolve(json(row()));
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
  });
});

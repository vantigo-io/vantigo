import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
// Sorted the way biome sorts: "-follow-ups-page" before "follow-ups" ('-' sorts
// before 'f'), and the side-effect i18n import last, which is where
// `customer-overview-route.test.tsx` already puts its own.
import { FollowUpsTab } from "./-follow-ups-page";
import { Route as FollowUpsRoute } from "./follow-ups";
import "../../i18n";

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/customers-ui/pages/follow-ups", () => ({
  FollowUpsPage: ({ canManageTimeline }: { canManageTimeline?: boolean }) => (
    <span data-testid="can-manage-timeline">{String(Boolean(canManageTimeline))}</span>
  ),
}));

const renderTab = (permissions: string[]) => {
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false } as never)
      : ({ data: { user: { id: "user-1", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <FollowUpsTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the Follow-ups route's search params", () => {
  const validate = FollowUpsRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

  it("defaults to my open follow-ups on the first page", () => {
    expect(validate({})).toEqual({ page: 1, assignee: "me", state: "open", customerId: undefined });
  });

  it("keeps only the filter values the API accepts", () => {
    expect(validate({ assignee: "none", state: "overdue", page: "3", customerId: "42" })).toEqual({
      page: 3,
      assignee: "none",
      state: "overdue",
      customerId: 42,
    });
    // 'Me' is case-sensitive at the API and a uuid is not one of the page's two
    // choices, so neither reaches the URL; an unknown state falls back rather
    // than narrowing the list by something the Select cannot show.
    expect(validate({ assignee: "Me", state: "bogus", customerId: "nope" })).toEqual({
      page: 1,
      assignee: "me",
      state: "open",
      customerId: undefined,
    });
  });
});

describe("the Follow-ups route's capability prop", () => {
  it("passes canManageTimeline from customers:timeline-manage", () => {
    renderTab(["customers:timeline-manage"]);
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("true");
  });

  it("withholds it without the permission", () => {
    renderTab(["customers:timeline-view"]);
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("false");
  });
});

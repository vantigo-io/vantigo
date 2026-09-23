import { createFileRoute } from "@tanstack/react-router";
import { followUpsListParams, followUpsQueryOptions } from "@vantigo/customers-ui/api/follow-ups";
import { FollowUpsTab } from "./-follow-ups-page";

const assignees = ["me", "none"] as const;
const states = ["open", "overdue", "done", "all"] as const;

/**
 * Both filters are URL search params (follow-ups design D3), so a filtered list
 * is a link. Anything the API would refuse is dropped here rather than
 * forwarded: an unknown state falls back to the default rather than narrowing
 * the rows by something the page's Select cannot show, and 'Me' is not 'me'
 * because the API matches case-sensitively.
 */
export const Route = createFileRoute("/customers/follow-ups")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    assignee: assignees.includes(search.assignee as (typeof assignees)[number])
      ? (search.assignee as (typeof assignees)[number])
      : ("me" as const),
    state: states.includes(search.state as (typeof states)[number])
      ? (search.state as (typeof states)[number])
      : ("open" as const),
    customerId: Number(search.customerId) > 0 ? Number(search.customerId) : undefined,
  }),
  loaderDeps: ({ search }) => followUpsListParams(search),
  loader: ({ context: { queryClient }, deps }) => queryClient.ensureQueryData(followUpsQueryOptions(deps)),
  component: FollowUpsTab,
});

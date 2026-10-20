import { createFileRoute, notFound } from "@tanstack/react-router";
import { GroupPage } from "@vantigo/staff-ui/pages/group";

/**
 * One group. The path is the package's own `GROUP_ROUTE_PATH` — Groups' rows,
 * New group, the Account tab's Groups card and the dashboard's
 * `groupSyncFailed` item link to it — and, as a person's, an id that is not
 * a positive whole number is the layout's not-found rather than
 * `GET /groups/NaN`.
 */
export const Route = createFileRoute("/staff/groups/$id")({
  beforeLoad: ({ params }) => {
    if (!/^\d+$/.test(params.id) || !Number.isSafeInteger(Number(params.id)) || Number(params.id) <= 0) {
      throw notFound();
    }
  },
  component: GroupPage,
});

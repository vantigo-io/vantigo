import type { QueryClient } from "@tanstack/react-query";
import { redirect } from "@tanstack/react-router";
import { MY_PLANNING_ROUTE_PATH } from "@vantigo/planning-ui/lib/routes";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The board, a project's plan, the requests and the intake are
 * `planning:view`'s (planning design §5), but they live under the app's
 * prefix, which the guard asks only `planning:access` of — a person's own
 * timeline is under it too. So these routes send a caller without
 * `planning:view` to My planning, the one destination every Planning user
 * has, as `/staff` sends one without the directory to My page. The
 * permissions are the guard's own query, usually cached; when they cannot be
 * read, My planning is the safe landing.
 */
export const requirePlanningView = async ({ context }: { context: { queryClient: QueryClient } }) => {
  const permissions = await context.queryClient
    .fetchQuery({ queryKey: ["authorization", "me", "none"], queryFn: getAuthorizationMe, staleTime: 300_000 })
    .then((authorization) => authorization.permissions)
    .catch(() => undefined);
  if (!hasPermissions(permissions, ["planning:view"])) throw redirect({ to: MY_PLANNING_ROUTE_PATH });
};

/** Whether a path parameter is a positive whole number, so `GET /…/NaN` never reaches the API. */
export const isPositiveId = (value: string) =>
  /^\d+$/.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0;

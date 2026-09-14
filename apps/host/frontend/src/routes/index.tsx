import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";

export const Route = createFileRoute("/")({
  // `/` is a pure redirect: system admins land on the control plane, everyone
  // else on the dashboard (task 3 of the frontend de-tenanting plan moved the
  // former tenant landing page to /dashboard). The tenant-slug redirect this
  // replaced was deleted with -tenant-routing.ts's activeTenantForSession.
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (session?.isSystemAdmin) throw redirect({ to: "/admin" });
    // /dashboard validates its search params, so TanStack requires them on
    // every navigation to it. These are the same values its validator derives
    // from an empty query string.
    throw redirect({ to: "/dashboard", search: { preset: "30d", from: undefined, to: undefined, metric: undefined } });
  },
});

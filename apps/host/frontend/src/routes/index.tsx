import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";

export const Route = createFileRoute("/")({
  // The active-tenant redirect was deleted with -tenant-routing.ts's
  // activeTenantForSession (task 2 of the frontend de-tenanting plan);
  // task 4 owns choosing this route's replacement destination for
  // non-system-admin sessions. System admins still land on the control
  // plane, unconditionally now that there is no tenant membership to check.
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (session?.isSystemAdmin) throw redirect({ to: "/admin" });
  },
});

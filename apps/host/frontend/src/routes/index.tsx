import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { activeTenantForSession } from "./-tenant-routing";

export const Route = createFileRoute("/")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    const activeTenant = activeTenantForSession(session);
    if (activeTenant) throw redirect({ href: `/${encodeURIComponent(activeTenant.slug)}` });
    // System admins without tenant access land on the control plane instead
    // of the "tenant required" screen (first-onboarding flow).
    if (session?.isSystemAdmin && (session.tenants ?? []).length === 0) throw redirect({ to: "/admin" });
  },
});

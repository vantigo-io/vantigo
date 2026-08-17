import { createFileRoute, notFound, Outlet, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey, switchTenant } from "../api/auth";
import { setActiveTenantSlug } from "../api/request";
import { synchronizeTenant } from "./-tenant-routing";

export const Route = createFileRoute("/$tenantSlug")({
  beforeLoad: async ({ context, params }) => {
    const session = await context.queryClient.fetchQuery({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300_000,
    });
    if (!session) throw redirect({ to: "/sign-in", search: { error: undefined } });

    const { session: updatedSession, tenant } = await synchronizeTenant(session, params.tenantSlug, switchTenant);
    if (!tenant) throw notFound();
    context.queryClient.setQueryData(sessionQueryKey, updatedSession);
    setActiveTenantSlug(tenant.slug);
  },
  component: () => <Outlet />,
});

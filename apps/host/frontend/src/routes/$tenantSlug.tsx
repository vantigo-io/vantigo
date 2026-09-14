import { createFileRoute, notFound, Outlet, redirect, useParams } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey, switchTenant } from "../api/auth";
import { TenantModuleGuard } from "../components/tenant-module-guard";
import { synchronizeTenant } from "./-tenant-routing";

const TenantLayout = () => {
  const { tenantSlug } = useParams({ from: "/$tenantSlug" });
  return (
    <TenantModuleGuard tenantSlug={tenantSlug}>
      <Outlet />
    </TenantModuleGuard>
  );
};

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
  },
  component: TenantLayout,
});

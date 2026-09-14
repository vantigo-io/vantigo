import { createFileRoute, Outlet, redirect, useParams } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { TenantModuleGuard } from "../components/tenant-module-guard";

const TenantLayout = () => {
  const { tenantSlug } = useParams({ from: "/$tenantSlug" });
  return (
    <TenantModuleGuard tenantSlug={tenantSlug}>
      <Outlet />
    </TenantModuleGuard>
  );
};

export const Route = createFileRoute("/$tenantSlug")({
  // Tenant-slug validation and session synchronization were deleted with
  // -tenant-routing.ts's synchronizeTenant and api/auth.ts's switchTenant
  // (task 2 of the frontend de-tenanting plan). Only the session check
  // remains; the slug segment itself is unvalidated until task 3 collapses
  // this route subtree up one level.
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({
      queryKey: sessionQueryKey,
      queryFn: fetchSession,
      staleTime: 300_000,
    });
    if (!session) throw redirect({ to: "/sign-in", search: { error: undefined } });
  },
  component: TenantLayout,
});

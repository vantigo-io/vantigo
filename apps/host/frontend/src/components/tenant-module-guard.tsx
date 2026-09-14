import { Center, Loader } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasPermissions, navSections } from "../navigation";
import { ForbiddenPage } from "./errors";

interface ModuleAccessRule {
  prefix: string;
  module: NonNullable<(typeof navSections)[number]["items"][number]["module"]>;
  requiredPermissions?: readonly string[];
}

// The navigation catalog is the single source of truth for which destinations
// belong to which module and which permissions they require.
const rules: ModuleAccessRule[] = navSections
  .flatMap((section) => section.items)
  .filter((item) => item.tenantScoped && item.module)
  .map((item) => ({
    prefix: item.to,
    module: item.module as ModuleAccessRule["module"],
    requiredPermissions: item.requiredPermissions,
  }))
  .sort((a, b) => b.prefix.length - a.prefix.length);

const moduleAccessRuleForSubPath = (subPath: string) =>
  rules.find((rule) => subPath === rule.prefix || subPath.startsWith(`${rule.prefix}/`));

/**
 * Guards tenant-scoped destinations in place: renders an access-denied page when
 * the user lacks every relevant permission. The backend enforces this
 * independently. (Module-enablement gating was removed with the deleted
 * tenant-capabilities endpoint — see api/auth.ts task 2 of the frontend
 * de-tenanting plan.)
 */
export const TenantModuleGuard = ({ tenantSlug, children }: { tenantSlug: string; children: ReactNode }) => {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const subPath = pathname.startsWith(`/${tenantSlug}`) ? pathname.slice(tenantSlug.length + 1) || "/" : pathname;
  const rule = moduleAccessRuleForSubPath(subPath);
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!rule && !!session,
    retry: false,
    staleTime: 300_000,
  });
  if (!rule) return children;
  if (authorization.isPending)
    return (
      <Center mih="50vh">
        <Loader size="sm" />
      </Center>
    );
  const allowed = hasPermissions(authorization.data?.permissions, rule.requiredPermissions);
  return allowed ? (
    children
  ) : (
    <ForbiddenPage
      requiredModule={rule.module}
      requiredPermissions={rule.requiredPermissions ? [...rule.requiredPermissions] : undefined}
    />
  );
};

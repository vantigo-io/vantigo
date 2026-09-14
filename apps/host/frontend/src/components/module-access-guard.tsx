import { Center, Loader } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasPermissions, type ModuleKey, navSections } from "../navigation";
import { ForbiddenPage } from "./errors";

interface ModuleAccessRule {
  prefix: string;
  module: ModuleKey;
  requiredPermissions?: readonly string[];
}

// The navigation catalog is the single source of truth for which destinations
// belong to which module and which permissions they require.
const rules: ModuleAccessRule[] = navSections
  .flatMap((section) => section.items)
  .flatMap((item) =>
    item.module
      ? [{ prefix: item.to, module: item.module, requiredPermissions: item.requiredPermissions }]
      : ([] as ModuleAccessRule[]),
  )
  .sort((a, b) => b.prefix.length - a.prefix.length);

const moduleAccessRuleForPath = (pathname: string) =>
  rules.find((rule) => pathname === rule.prefix || pathname.startsWith(`${rule.prefix}/`));

/**
 * Guards module destinations in place: renders an access-denied page when the
 * user lacks every relevant permission. The backend enforces this
 * independently.
 *
 * This is the surviving permission half of the former tenant module guard; its
 * module-enablement half went with the deleted tenant-capabilities endpoint
 * (task 2 of the frontend de-tenanting plan) and the tenant path prefix went
 * with the route collapse (task 3). It is mounted on the root layout, which is
 * where the deleted tenant layout route used to mount it.
 */
export const ModuleAccessGuard = ({ children }: { children: ReactNode }) => {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const rule = moduleAccessRuleForPath(pathname);
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

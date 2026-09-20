import { Center, Loader } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { ForbiddenPage, ModuleNotEnabledPage, UnexpectedErrorPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions, type ModuleKey } from "../../navigation";
import "../../i18n";

export interface ModuleTabGateProps {
  /** The module the tab's API belongs to; without it the tab does not exist. */
  module: ModuleKey;
  /** The one permission that module's API demands of every operation. */
  permission: string;
  /** The app's own name, for the not-enabled page. */
  appLabel: string;
  children: ReactNode;
}

/**
 * The two gates a project-page tab from **another module** carries, in one
 * place, so a pasted URL is answered the same way the tab row is.
 *
 * `ModuleAccessGuard` cannot do this: it matches URL prefixes from the app
 * registry, and these tabs live under `/projects`, whose rule is Projects'
 * own. So the tab itself has to ask. The module gate keeps a link from
 * reaching an API that is not mounted; the permission gate keeps it from
 * reaching one that will refuse every read — which, without this, is a page of
 * red alerts under a tab strip the tab is not even in.
 *
 * Whether the caller may see *this project's* figures is not asked here: that
 * is the module's own answer, shaped per project, and the panels say it in
 * their own words.
 *
 * A permission read that **failed** is not a permission that was refused: it
 * gets the retry an error page offers, not "Access denied", which would tell
 * somebody they lack a right they may well hold.
 */
export const ModuleTabGate = ({ module, permission, appLabel, children }: ModuleTabGateProps) => {
  const { data: session } = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session,
    retry: false,
    staleTime: 300_000,
  });
  if (!enabledModuleKeys().includes(module)) return <ModuleNotEnabledPage appLabel={appLabel} />;
  // The layout above has already asked for both of these, so on a click they
  // are warm and this never paints; on a pasted URL it is one short wait
  // rather than a panel that appears and is then taken away.
  if (authorization.isError)
    return <UnexpectedErrorPage error={authorization.error} reset={() => void authorization.refetch()} />;
  if (authorization.isPending)
    return (
      <Center mih="30vh">
        <Loader size="sm" />
      </Center>
    );
  if (!hasPermissions(authorization.data?.permissions, [permission]))
    return <ForbiddenPage requiredModule={module} requiredPermissions={[permission]} />;
  return children;
};

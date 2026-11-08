import { useQuery } from "@tanstack/react-query";
import type { AgentsAccess } from "@vantigo/agents-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";

/**
 * What the Agents pages may offer, judged from the caller's permissions:
 * `agents:manage` creates, edits, pauses and deletes. Raising a level, an Auto
 * mode and binding a channel are the Owner's besides (`isOwner`, which the
 * pages use to disable those controls); the API decides too.
 */
export const agentsAccessOf = (permissions: string[] | undefined, isOwner = false): AgentsAccess => ({
  canManage: hasAllPermissions(permissions, ["agents:manage"]),
  isOwner,
});

/** The Agents pages' access from the root layout's session and authorization, and whether it has answered. */
export const useAgentsAccess = (): { ready: boolean; access: AgentsAccess } => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return {
    ready: authorization.isSuccess || authorization.isError,
    access: agentsAccessOf(authorization.data?.permissions, authorization.data?.administrationScope.isOwner === true),
  };
};

import { useQuery } from "@tanstack/react-query";
import type { KnowledgePageAccess } from "@vantigo/knowledge-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";

/**
 * What the Knowledge pages may offer beyond each article's own access
 * (knowledge design K2): `knowledge:manage` restores anyone's trash and
 * empties it. Every Knowledge call asks for knowledge:articles too.
 */
export const knowledgeAccessOf = (permissions: string[] | undefined): KnowledgePageAccess => ({
  canManage: hasAllPermissions(permissions, ["knowledge:articles", "knowledge:manage"]),
});

/** The Knowledge pages' access from the root layout's session and authorization, and whether it has answered. */
export const useKnowledgeAccess = (): { ready: boolean; access: KnowledgePageAccess } => {
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
    access: knowledgeAccessOf(authorization.data?.permissions),
  };
};

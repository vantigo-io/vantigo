import { useQuery } from "@tanstack/react-query";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";

/**
 * The caller's permissions, for the capability props the energy pages take —
 * the host reads permissions, the package never fetches them itself. The same
 * query keys the root layout uses, so this reads its cache.
 */
export const useEnergyPermissions = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return authorization.data?.permissions;
};

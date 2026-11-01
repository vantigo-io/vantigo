import { useQuery } from "@tanstack/react-query";
import { ProjectTemplatesPage as PackageProjectTemplatesPage } from "@vantigo/projects-ui/pages/templates";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The project template catalog. The page is the package's; whether this
 * caller may create, edit and delete templates is the host's answer — it owns
 * the session and the authorization query — so it is computed here and passed
 * in, exactly as the project list passes `canCreate`. The API asks the same
 * permission, projects:create, of every template write.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectTemplatesPage = () => {
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return (
    <PackageProjectTemplatesPage canManage={hasPermissions(authorization.data?.permissions, ["projects:create"])} />
  );
};

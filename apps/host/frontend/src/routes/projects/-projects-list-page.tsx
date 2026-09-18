import { useQuery } from "@tanstack/react-query";
import { ProjectsPage } from "@vantigo/projects-ui/pages/projects.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The Projects app's list page. The list itself is the package's; whether this
 * caller may create a project is the host's answer — it owns the session and
 * the authorization query — so it is computed here and passed in, exactly as
 * the customer page's Projects tab does it.
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectsListPage = () => {
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return <ProjectsPage canCreate={hasPermissions(authorization.data?.permissions, ["projects:create"])} />;
};

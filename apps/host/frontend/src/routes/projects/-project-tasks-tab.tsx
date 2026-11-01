import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ProjectTasks } from "@vantigo/projects-ui/pages/project-tasks";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The project's Tasks tab. The tab itself is the package's; the session user
 * and the task the URL deep-links to are the host's answers — it owns the
 * session and the router — so both are computed here and passed in, exactly as
 * the list page passes `canCreate`.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const ProjectTasksTab = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const { task } = useSearch({ from: "/projects/$projectId/tasks" });
  const navigate = useNavigate();
  // The same session key the root layout uses, so this reads its cache. The
  // package needs the caller's id only to know whose comments are their own.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  // Whether Use template may point the caller at the template catalog, which
  // only projects:create may add to.
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });

  return (
    <ProjectTasks
      projectId={projectId}
      currentUserId={session.data?.user.id}
      openTaskId={task}
      canManageTemplates={hasPermissions(authorization.data?.permissions, ["projects:create"])}
      // Opening a task is a place the caller can go back from; closing one
      // consumes the intent instead, so neither a refresh nor Back reopens a
      // drawer the caller just shut.
      onOpenTaskChange={(taskId) =>
        void navigate({
          to: "/projects/$projectId/tasks",
          params: { projectId },
          search: taskId === undefined ? {} : { task: taskId },
          replace: taskId === undefined,
        })
      }
    />
  );
};

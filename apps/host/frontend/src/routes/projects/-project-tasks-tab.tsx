import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ProjectTasks } from "@vantigo/projects-ui/pages/project-tasks";
import { fetchSession, sessionQueryKey } from "../../api/auth";

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

  return (
    <ProjectTasks
      projectId={projectId}
      currentUserId={session.data?.user.id}
      openTaskId={task}
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

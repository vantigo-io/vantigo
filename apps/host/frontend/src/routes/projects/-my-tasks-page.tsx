import { useNavigate, useSearch } from "@tanstack/react-router";
import { MyTasksPage as PackageMyTasksPage } from "@vantigo/projects-ui/pages/my-tasks";
import { CreateTaskModal } from "./-create-task-modal";

/**
 * The caller's open tasks across every project they see. The page is the
 * package's; the `create` intent Spotlight's "Create task" action arrives with
 * is the host's, because the project it belongs to is picked here.
 *
 * It sits beside the route file rather than inside it because a route file may
 * export nothing but its `Route` without costing the bundle a code split.
 */
export const MyTasksPage = () => {
  const { create } = useSearch({ from: "/projects/my-tasks" });
  const navigate = useNavigate();

  return (
    <>
      <PackageMyTasksPage />
      <CreateTaskModal
        opened={create === true}
        // The intent is consumed: it must not reopen the picker on refresh or Back.
        onClose={() => void navigate({ to: "/projects/my-tasks", search: {}, replace: true })}
      />
    </>
  );
};

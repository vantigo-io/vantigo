import { createFileRoute } from "@tanstack/react-router";
import { ProjectTemplatesPage } from "@vantigo/projects-ui/pages/templates";

// A static segment beside /projects/$projectId, the way economy.tsx and
// my-tasks.tsx sit beside it; TanStack ranks the literal segment above the
// dynamic one, so this page is never asked for as the project "templates".
// The page needs no permission of the host's: the navigation entry and the
// module access guard already ask projects:create of anyone who reaches it,
// the permission the API checks every template write against.
export const Route = createFileRoute("/projects/templates")({
  component: ProjectTemplatesPage,
});

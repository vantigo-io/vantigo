import { createFileRoute } from "@tanstack/react-router";
import { ProjectTemplatesPage } from "./-project-templates-page";

// A static segment beside /projects/$projectId, the way economy.tsx and
// my-tasks.tsx sit beside it; TanStack ranks the literal segment above the
// dynamic one, so this page is never asked for as the project "templates".
export const Route = createFileRoute("/projects/templates")({
  component: ProjectTemplatesPage,
});

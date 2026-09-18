import { createFileRoute } from "@tanstack/react-router";
import { MyTasksPage } from "./-my-tasks-page";

/** The page's URL search params: `create` only ever arrives as true, from Spotlight. */
interface MyTasksSearch {
  create?: true;
}

const flag = (value: unknown) => value === true || value === "true";

export const Route = createFileRoute("/projects/my-tasks")({
  // A static segment beside /projects/$projectId; TanStack ranks it above the
  // dynamic one, so this page is never asked for as the project "my-tasks".
  validateSearch: (search: Record<string, unknown>): MyTasksSearch =>
    flag(search.create) ? { create: true as const } : {},
  component: MyTasksPage,
});

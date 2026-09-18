import { createFileRoute } from "@tanstack/react-router";
import { projectStatsQueryOptions, projectsQueryOptions } from "@vantigo/projects-ui/api/projects";
import { isProjectStatus } from "@vantigo/projects-ui/lib/status";
import { ProjectsPage, type ProjectsSearch } from "@vantigo/projects-ui/pages/projects.index";

const flag = (value: unknown) => value === true || value === "true";

export const Route = createFileRoute("/projects/")({
  // The list page reads these back with useSearch, so every filter survives a
  // refresh and a pasted link. An unknown status falls back to "any" rather
  // than reaching the API as a value it would reject.
  validateSearch: (search: Record<string, unknown>): ProjectsSearch => {
    const status = String(search.status ?? "");
    return {
      page: Math.max(1, Number(search.page) || 1),
      search: typeof search.search === "string" ? search.search : "",
      status: isProjectStatus(status) ? status : "",
      customerId: search.customerId === undefined ? undefined : Number(search.customerId),
      internal: search.internal === undefined ? undefined : flag(search.internal),
      mine: flag(search.mine),
      // Present only when true: the create form opens on arrival (Spotlight's quick action).
      ...(flag(search.create) ? { create: true as const } : {}),
    };
  },
  loaderDeps: ({ search }) => ({
    page: search.page,
    search: search.search,
    status: search.status,
    customerId: search.customerId,
    internal: search.internal,
    mine: search.mine,
  }),
  loader: ({ context: { queryClient }, deps }) =>
    Promise.all([
      queryClient.ensureQueryData(projectsQueryOptions(deps)),
      queryClient.ensureQueryData(projectStatsQueryOptions()),
    ]),
  component: ProjectsPage,
});

import { createFileRoute, notFound } from "@tanstack/react-router";
import { NotFoundError, projectQueryOptions } from "@vantigo/projects-ui/api/projects";
import { ProjectDetailLayout } from "./-project-detail-layout";

export const Route = createFileRoute("/projects/$projectId")({
  params: {
    parse: ({ projectId }) => ({ projectId: Number(projectId) }),
    stringify: ({ projectId }) => ({ projectId: String(projectId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(projectQueryOptions(params.projectId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: ProjectDetailLayout,
});

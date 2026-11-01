import { createFileRoute, notFound, redirect } from "@tanstack/react-router";
import { aiSettingsQueryKey, fetchAiSettings } from "../../api/ai";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import type { ApiError } from "../../api/request";
import { NotFoundPage } from "../../components/errors";
import { AiModelsPage } from "./-ai-models";

/**
 * Integrations › AI models (Integrations design T13): the Owner's, and present
 * only while the instance has an AI provider. The settings answer is the proof:
 * a 404 means there is none, and the route answers not-found as an unknown URL
 * does; any other failure lets the page render and say it could not load.
 */
export const Route = createFileRoute("/workspace/ai")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
    try {
      await context.queryClient.fetchQuery({
        queryKey: aiSettingsQueryKey,
        queryFn: fetchAiSettings,
        staleTime: 300_000,
      });
    } catch (error) {
      if ((error as ApiError | undefined)?.status === 404) throw notFound();
    }
  },
  notFoundComponent: NotFoundPage,
  component: AiModelsPage,
});

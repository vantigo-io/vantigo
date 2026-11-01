import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { moduleDisabledNotFound } from "../-app-layout";
import { AssistantSettingsPage } from "./-assistant-settings";

/**
 * Workspace admin › Assistant (Assistant design S8, S16): the Owner's, and present while the module is on.
 * An instance with no AI provider still opens the page, which says so rather than hiding it.
 */
export const Route = createFileRoute("/workspace/assistant")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
    if (!enabledModuleKeys().includes("assistant")) throw moduleDisabledNotFound("assistant");
  },
  component: AssistantSettingsPage,
});

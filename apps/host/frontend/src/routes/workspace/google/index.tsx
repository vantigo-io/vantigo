import { createFileRoute, redirect } from "@tanstack/react-router";
import { connectionQueryOptions, GoogleStatusTab, opensOnConnection } from "@vantigo/google-ui";

/**
 * The Status tab. While nothing is set up and no key is uploaded there is no
 * status to show, so the page opens on Connection, as Entra's does. A
 * connection that cannot be read opens the tab itself, which says so and
 * offers to try again.
 */
export const Route = createFileRoute("/workspace/google/")({
  beforeLoad: async ({ context }) => {
    const connection = await context.queryClient.ensureQueryData(connectionQueryOptions()).catch(() => undefined);
    if (connection && opensOnConnection(connection)) {
      throw redirect({ to: "/workspace/google/connection", replace: true });
    }
  },
  component: GoogleStatusTab,
});

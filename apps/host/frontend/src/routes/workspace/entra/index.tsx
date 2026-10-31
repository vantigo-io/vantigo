import { createFileRoute, redirect } from "@tanstack/react-router";
import { connectionQueryOptions, EntraStatusTab, stepsDone } from "@vantigo/entra-ui";

/**
 * The Status tab. Onboarding without an onboarding page (Integrations design
 * T3): while step 1 is not done there is nothing to show a status of, so the
 * page opens on Connection. A connection that cannot be read opens the tab
 * itself, which says so and offers to try again.
 */
export const Route = createFileRoute("/workspace/entra/")({
  beforeLoad: async ({ context }) => {
    const connection = await context.queryClient.ensureQueryData(connectionQueryOptions()).catch(() => undefined);
    if (connection && !stepsDone(connection).register) {
      throw redirect({ to: "/workspace/entra/connection", replace: true });
    }
  },
  component: EntraStatusTab,
});

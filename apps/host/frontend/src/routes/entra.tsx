import { createFileRoute, redirect } from "@tanstack/react-router";

/**
 * `/entra` is where the Entra page was while it had a tile in the app switcher.
 * It is `/workspace/entra` now, under Integrations in Workspace admin
 * (Integrations design T2, T3); the old address redirects for good, as Staff's
 * and Communications' links and both languages' documentation reach the page by
 * site path. The Status tab sends a connection that is not registered yet on to
 * Connection by itself.
 */
export const Route = createFileRoute("/entra")({
  beforeLoad: () => {
    throw redirect({ to: "/workspace/entra", replace: true });
  },
});

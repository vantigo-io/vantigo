import { createFileRoute } from "@tanstack/react-router";
import { GoogleAccountsTab } from "@vantigo/google-ui";

/** The accounts and groups, still to come; a route from the start so links never move. */
export const Route = createFileRoute("/workspace/google/accounts")({
  component: GoogleAccountsTab,
});

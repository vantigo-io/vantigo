import { createFileRoute } from "@tanstack/react-router";
import { GoogleAccountsTab } from "@vantigo/google-ui";

/** The account rules, the licences, and the users and groups the module keeps for Staff. */
export const Route = createFileRoute("/workspace/google/accounts")({
  component: GoogleAccountsTab,
});

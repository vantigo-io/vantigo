import { createFileRoute } from "@tanstack/react-router";
import { GroupsPage } from "@vantigo/staff-ui/pages/groups";

/** The groups (Entra delivery D), staff:manage's; the package's `GROUPS_ROUTE_PATH`. */
export const Route = createFileRoute("/staff/groups/")({
  component: GroupsPage,
});

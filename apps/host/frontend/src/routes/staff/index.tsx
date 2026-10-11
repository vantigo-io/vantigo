import { createFileRoute, redirect } from "@tanstack/react-router";
import { MY_PAGE_ROUTE_PATH } from "@vantigo/staff-ui/lib/routes";

// The staff app prefix is a pure redirect: My page is the one destination
// every Staff user is offered (`staff:access`); the directory and the
// settings are in the sidebar for those who hold more.
export const Route = createFileRoute("/staff/")({
  beforeLoad: () => {
    throw redirect({ to: MY_PAGE_ROUTE_PATH, search: { tab: undefined } });
  },
});

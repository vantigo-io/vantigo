import { createFileRoute, Outlet } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";

/**
 * The personal settings area: every signed-in user, with the Profile and
 * Security pages in the sidebar (see `areas` in apps.ts). The MFA enrolment
 * gate holds an administrator here, so the area stays open while the root
 * withholds the sidebar.
 */
export const Route = createFileRoute("/settings")({
  staticData: { app: "settings" },
  beforeLoad: async ({ context }) => {
    await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  },
  component: Outlet,
});

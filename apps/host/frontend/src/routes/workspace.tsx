import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";

/**
 * The workspace administration area. Admits anyone who may see one of its
 * pages: Owners (overview, users, invitations) and authorization managers
 * (roles and access). Each child route keeps its own, stricter gate, and the
 * sidebar (see `areas` in apps.ts) filters by the same flags.
 */
export const Route = createFileRoute("/workspace")({
  staticData: { app: "workspace" },
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (session?.user.roles.includes("Owner")) return;
    const access = session
      ? await context.queryClient.fetchQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe })
      : undefined;
    if (!access?.canManageAuthorization) throw redirect({ to: "/" });
  },
  component: Outlet,
});

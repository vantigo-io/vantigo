import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { enabledModuleKeys } from "../lib/enabled-modules";
import { hasPermissions } from "../navigation";

/**
 * The workspace administration area. Admits anyone who may see one of its
 * pages: Owners (overview, users, invitations), authorization managers (roles
 * and access) and, while the Entra module is on, holders of `entra:manage`
 * (Integrations › Microsoft Entra ID), and, while the Google module is on,
 * holders of `google:manage` (Integrations › Google Workspace). Each child
 * route keeps its own, stricter gate, and the sidebar (see `areas` in apps.ts) filters by the same
 * flags, so a person admitted for one page sees only the sections it belongs to.
 */
export const Route = createFileRoute("/workspace")({
  staticData: { app: "workspace" },
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (session?.user.roles.includes("Owner")) return;
    const access = session
      ? await context.queryClient.fetchQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe })
      : undefined;
    if (access?.canManageAuthorization) return;
    if (enabledModuleKeys().includes("entra") && hasPermissions(access?.permissions, ["entra:manage"])) return;
    if (enabledModuleKeys().includes("google") && hasPermissions(access?.permissions, ["google:manage"])) return;
    throw redirect({ to: "/" });
  },
  component: Outlet,
});

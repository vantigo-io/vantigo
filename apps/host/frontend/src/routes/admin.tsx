import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";

/** The system administration area: one page today, so no sidebar (see `areas` in apps.ts). */
export const Route = createFileRoute("/admin")({
  staticData: { app: "admin" },
  beforeLoad: async ({ context }) => {
    const s = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!s?.isSystemAdmin) throw redirect({ to: "/" });
  },
  component: Outlet,
});

import { createFileRoute, Outlet } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../api/auth";

export const Route = createFileRoute("/admin")({
  beforeLoad: async ({ context }) => {
    const s = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!s?.isSystemAdmin) throw (await import("@tanstack/react-router")).redirect({ to: "/" });
  },
  component: () => <Outlet />,
});

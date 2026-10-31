import { createFileRoute, redirect } from "@tanstack/react-router";

/** `/entra/`, as `/entra`: the page is `/workspace/entra` (see `../entra.tsx`). */
export const Route = createFileRoute("/entra/")({
  beforeLoad: () => {
    throw redirect({ to: "/workspace/entra", replace: true });
  },
});

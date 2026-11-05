import { createFileRoute, redirect } from "@tanstack/react-router";

// The Orders app prefix is a pure redirect: the quotes are its home (orders
// design O11), and every destination of the app asks for orders:access, which
// the guard asks of the whole prefix.
export const Route = createFileRoute("/orders/")({
  beforeLoad: () => {
    throw redirect({ to: "/orders/quotes" });
  },
});

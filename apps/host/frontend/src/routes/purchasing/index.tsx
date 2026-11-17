import { createFileRoute, redirect } from "@tanstack/react-router";

// The Purchasing app prefix is a pure redirect: the purchase orders are its
// home (purchasing design P23), and every destination of the app asks for
// purchasing:access, which the guard asks of the whole prefix.
export const Route = createFileRoute("/purchasing/")({
  beforeLoad: () => {
    throw redirect({ to: "/purchasing/orders" });
  },
});

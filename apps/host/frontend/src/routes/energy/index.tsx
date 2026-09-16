import { createFileRoute, redirect } from "@tanstack/react-router";

// The energy app prefix is a pure redirect: metering points are its home.
export const Route = createFileRoute("/energy/")({
  beforeLoad: () => {
    throw redirect({ to: "/energy/metering-points", search: { page: 1, search: "" } });
  },
});

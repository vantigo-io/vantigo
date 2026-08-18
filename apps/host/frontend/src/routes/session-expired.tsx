import { createFileRoute } from "@tanstack/react-router";
import { SessionExpiredPage } from "../components/errors";

export const Route = createFileRoute("/session-expired")({
  validateSearch: (search: Record<string, unknown>) => ({
    returnTo: typeof search.returnTo === "string" ? search.returnTo : undefined,
  }),
  component: SessionExpiredPage,
});

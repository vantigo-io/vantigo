import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { RsvpPage } from "@vantigo/calendar-ui/pages/rsvp";

/**
 * The public RSVP page (calendar invitations design D5, D7): an invitation's
 * `/rsvp?token=…`. `/rsvp` is in `publicPaths` and `sessionlessPaths`, so the
 * root asks for no session, authorization, profile or maintenance status here
 * — on the public host every request the page makes must be on
 * `security.PayRoutes()`'s allowlist (`pay.test.tsx` holds it to that).
 */
export const Route = createFileRoute("/rsvp")({
  component: RsvpRoute,
});

function RsvpRoute() {
  const navigate = useNavigate();
  // The token leaves the address bar through the router, so its own location agrees with the browser's.
  return <RsvpPage clearAddress={() => void navigate({ to: "/rsvp", search: {}, replace: true })} />;
}

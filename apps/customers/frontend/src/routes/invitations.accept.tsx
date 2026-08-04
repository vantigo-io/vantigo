import { createFileRoute } from "@tanstack/react-router";
import { AcceptInvitationPage } from "./accept-invitation";

const InvitationAcceptAliasRoute = () => <AcceptInvitationPage token={Route.useSearch().token} />;
export const Route = createFileRoute("/invitations/accept")({
  validateSearch: (search: Record<string, unknown>) => ({ token: String(search.token ?? "") }),
  component: InvitationAcceptAliasRoute,
});

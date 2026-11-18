import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import "../../i18n";
import { IntegrationPage } from "./-api-access-integration";

const IntegrationRoute = () => {
  const { integrationId } = Route.useParams();
  return <IntegrationPage integrationId={integrationId} />;
};

export const Route = createFileRoute("/workspace/api-access_/$integrationId")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: IntegrationRoute,
});

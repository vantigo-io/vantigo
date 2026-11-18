import { createFileRoute, redirect } from "@tanstack/react-router";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import "../../i18n";
import { IntegrationPage, type IntegrationTab } from "./-api-access-integration";

const IntegrationRoute = () => {
  const { integrationId } = Route.useParams();
  const { tab } = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <IntegrationPage
      integrationId={integrationId}
      tab={tab}
      onTab={(next) => void navigate({ search: next === "overview" ? {} : { tab: next }, replace: true })}
    />
  );
};

export const Route = createFileRoute("/workspace/api-access_/$integrationId")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  validateSearch: (search: Record<string, unknown>): { tab?: IntegrationTab } =>
    search.tab === "requests" ? { tab: "requests" } : {},
  component: IntegrationRoute,
});

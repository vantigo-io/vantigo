import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useParams } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { CustomerProjectsPanel } from "@vantigo/projects-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import "../../i18n";

// Lives in the customers app but calls the projects API, so it gates on the
// projects module itself: a pasted link must not hit an API 404.
const CustomerProjectsRoute = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The same keys the root layout uses, so this reads its cache; the panel's
  // create button is the host's call, as the package takes it as a prop.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  if (!enabledModuleKeys().includes("projects")) return <ModuleNotEnabledPage appLabel={t("navigation.projects")} />;
  return (
    <CustomerProjectsPanel
      customerId={customerId}
      canCreate={hasPermissions(authorization.data?.permissions, ["projects:create"])}
    />
  );
};

export const Route = createFileRoute("/customers/$customerId/projects")({
  component: CustomerProjectsRoute,
});

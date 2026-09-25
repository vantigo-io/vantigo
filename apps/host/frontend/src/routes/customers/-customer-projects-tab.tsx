import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { isReadOnlyCustomer } from "@vantigo/customers-ui/lib/customer-read-only";
import { useI18n } from "@vantigo/frontend-shell";
import { CustomerProjectsPanel } from "@vantigo/projects-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import "../../i18n";

/**
 * The customer page's Projects tab. It lives in the customers app but calls
 * the projects API, so it gates on the projects module itself: a pasted link
 * must not hit an API 404. The panel is the package's; whether this caller may
 * create a project from it is the host's answer, so it is passed in: with
 * `projects:create`, and never on a customer that takes no more changes
 * (`isReadOnlyCustomer`): a merged-away one (customers merge design D4) — a
 * project created there would sit on a customer nobody looks at, and no later
 * merge would re-point it — or an anonymised one (GDPR design D4), where the
 * projects API would take it (an archived customer is allowed) and a project
 * named in free text would land, for good, on "Anonymised person".
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const CustomerProjectsTab = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  // The layout's loader has already put the customer in the cache.
  const customer = useQuery(customerQueryOptions(customerId));
  if (!enabledModuleKeys().includes("projects")) return <ModuleNotEnabledPage appLabel={t("navigation.projects")} />;
  return (
    <CustomerProjectsPanel
      customerId={customerId}
      canCreate={
        hasPermissions(authorization.data?.permissions, ["projects:create"]) && !isReadOnlyCustomer(customer.data)
      }
    />
  );
};

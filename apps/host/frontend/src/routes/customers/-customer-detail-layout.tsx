import { Button } from "@mantine/core";
import { IconBolt, IconBriefcase, IconLayoutDashboard, IconMessages } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useMatches, useNavigate, useParams } from "@tanstack/react-router";
import { CustomerDetailHeader } from "@vantigo/customers-ui/pages/customers.$customerId";
import { PageTabs, useI18n } from "@vantigo/frontend-shell";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions, type ModuleKey } from "../../navigation";
import "../../i18n";

/** What a customer-page entry needs before the caller may see it. */
interface CustomerDetailGate {
  /** The module that must be enabled for the tenant; omitted = always shown. */
  module?: ModuleKey;
  /** Any one of these grants the entry; omitted = no permission needed. */
  requiredPermissions?: readonly string[];
}

type CustomerDetailView = "overview" | "energy" | "projects";

interface CustomerDetailTab extends CustomerDetailGate {
  value: CustomerDetailView;
  labelKey: "customer.overviewTab" | "customer.energyTab" | "customer.projectsTab";
  icon: typeof IconLayoutDashboard;
  to: "/customers/$customerId" | "/customers/$customerId/energy" | "/customers/$customerId/projects";
}

/** The views of the customer page, each a child route, so the tab row follows the URL. */
export const customerDetailTabs: CustomerDetailTab[] = [
  {
    value: "overview",
    labelKey: "customer.overviewTab",
    icon: IconLayoutDashboard,
    to: "/customers/$customerId",
  },
  {
    value: "energy",
    labelKey: "customer.energyTab",
    icon: IconBolt,
    to: "/customers/$customerId/energy",
    module: "energy",
    requiredPermissions: ["energy:metering-points-view"],
  },
  {
    value: "projects",
    labelKey: "customer.projectsTab",
    icon: IconBriefcase,
    to: "/customers/$customerId/projects",
    module: "projects",
    requiredPermissions: ["projects:access"],
  },
];

/**
 * Header actions that lead out of the customer page. Correspondence lives in
 * the Communications inbox, so it is an action here rather than a tab: a tab
 * that leaves the page could never be the active one.
 */
export const customerDetailActions = {
  correspondence: { module: "communications", requiredPermissions: ["communications:conversations-view"] },
} as const satisfies Record<string, CustomerDetailGate>;

const passesGate = (
  gate: CustomerDetailGate,
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
) =>
  (gate.module === undefined || enabledModules?.includes(gate.module) === true) &&
  hasPermissions(permissions, gate.requiredPermissions);

/** The tabs the caller may see: module enabled for the tenant and permission granted. */
export const visibleCustomerDetailTabs = (
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
): CustomerDetailTab[] => customerDetailTabs.filter((tab) => passesGate(tab, enabledModules, permissions));

/** Whether the "Open in inbox" action shows, under the same rules as a tab. */
export const showCorrespondenceAction = (
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
) => passesGate(customerDetailActions.correspondence, enabledModules, permissions);

export const CustomerDetailLayout = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  const matches = useMatches();
  const navigate = useNavigate();
  // These queries share the root layout's keys, so they read its cache rather
  // than refetching; the tab row simply follows the same visibility rules as
  // the sidebar navigation.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const enabledModules = enabledModuleKeys();
  const permissions = authorization.data?.permissions;
  const visibleTabs = visibleCustomerDetailTabs(enabledModules, permissions);
  const activeTab: CustomerDetailView = matches.some((match) => match.routeId === "/customers/$customerId/energy")
    ? "energy"
    : matches.some((match) => match.routeId === "/customers/$customerId/projects")
      ? "projects"
      : "overview";

  return (
    <>
      <CustomerDetailHeader
        customerId={customerId}
        canArchive={hasPermissions(permissions, ["customers:delete"])}
        canRestore={hasPermissions(permissions, ["customers:update"])}
        actions={
          showCorrespondenceAction(enabledModules, permissions) ? (
            <Button
              variant="default"
              leftSection={<IconMessages size={16} />}
              // renderRoot keeps the route-typed Link (search is validated by
              // the inbox route); `component={Link}` would erase that typing.
              renderRoot={(props) => (
                <Link
                  to="/communications/inbox"
                  search={{
                    customerId,
                    conversationId: undefined,
                    status: undefined,
                    tagId: undefined,
                    unreadOnly: undefined,
                  }}
                  {...props}
                />
              )}
            >
              {t("customer.openInInbox")}
            </Button>
          ) : undefined
        }
      />
      {visibleTabs.length > 1 && (
        <PageTabs
          aria-label={t("customer.views")}
          items={visibleTabs.map(({ value, labelKey, icon }) => ({ value, label: t(labelKey), icon }))}
          value={activeTab}
          onChange={(value) => {
            const tab = visibleTabs.find((item) => item.value === value);
            if (tab) void navigate({ to: tab.to, params: { customerId } });
          }}
        />
      )}
      <Outlet />
    </>
  );
};

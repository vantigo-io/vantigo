import { Tabs } from "@mantine/core";
import { IconBolt, IconLayoutDashboard, IconMessages } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Outlet, useMatches, useNavigate, useParams } from "@tanstack/react-router";
import { CustomerDetailHeader } from "@vantigo/customers-ui/pages/customers.$customerId";
import { useI18n } from "@vantigo/frontend-shell";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions, type ModuleKey } from "../../navigation";
import "../../i18n";

type CustomerDetailTab = {
  value: "overview" | "energy" | "correspondence";
  labelKey: "customer.overviewTab" | "customer.energyTab" | "customer.correspondenceTab";
  icon: typeof IconLayoutDashboard;
  to: "/customers/$customerId" | "/customers/$customerId/energy";
  /** The module that must be enabled for the tenant; omitted = always shown. */
  module?: ModuleKey;
  /** Any one of these grants the tab; omitted = no permission needed. */
  requiredPermissions?: readonly string[];
};

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
    value: "correspondence",
    labelKey: "customer.correspondenceTab",
    icon: IconMessages,
    to: "/customers/$customerId",
    module: "communications",
    requiredPermissions: ["communications:conversations-view"],
  },
];

/** The tabs the caller may see: module enabled for the tenant and permission granted. */
export const visibleCustomerDetailTabs = (
  enabledModules: readonly ModuleKey[] | undefined,
  permissions: string[] | undefined,
): CustomerDetailTab[] =>
  customerDetailTabs.filter(
    (tab) =>
      (tab.module === undefined || enabledModules?.includes(tab.module) === true) &&
      hasPermissions(permissions, tab.requiredPermissions),
  );

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
  const visibleTabs = visibleCustomerDetailTabs(enabledModuleKeys(), authorization.data?.permissions);
  const activeTab = matches.some((match) => match.routeId === "/customers/$customerId/energy") ? "energy" : "overview";

  return (
    <>
      <CustomerDetailHeader customerId={customerId} />
      {visibleTabs.length > 1 && (
        <Tabs
          value={activeTab}
          onChange={(value) => {
            const tab = visibleTabs.find((item) => item.value === value);
            if (tab)
              void (navigate as (options: unknown) => void)(
                tab.value === "correspondence"
                  ? { to: "/inbox", search: { customerId: String(customerId) } }
                  : { to: tab.to, params: { customerId } },
              );
          }}
        >
          <Tabs.List>
            {visibleTabs.map(({ value, labelKey, icon: Icon }) => (
              <Tabs.Tab key={value} value={value} leftSection={<Icon size={16} />}>
                {t(labelKey)}
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
      )}
      <Outlet />
    </>
  );
};

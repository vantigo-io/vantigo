import { Tabs } from "@mantine/core";
import { IconBolt, IconLayoutDashboard, IconMessages } from "@tabler/icons-react";
import { createFileRoute, notFound, Outlet, useMatches, useNavigate, useParams } from "@tanstack/react-router";
import { customerQueryOptions, NotFoundError } from "@vantigo/customers-ui/api/customers";
import { CustomerDetailHeader } from "@vantigo/customers-ui/pages/customers.$customerId";
import { useI18n } from "@vantigo/frontend-shell";
import "../../../i18n";

type CustomerDetailTab = {
  value: "overview" | "energy" | "correspondence";
  labelKey: "customer.overviewTab" | "customer.energyTab" | "customer.correspondenceTab";
  icon: typeof IconLayoutDashboard;
  to: "/$tenantSlug/customers/$customerId" | "/$tenantSlug/customers/$customerId/energy";
};

export const customerDetailTabs: CustomerDetailTab[] = [
  {
    value: "overview",
    labelKey: "customer.overviewTab",
    icon: IconLayoutDashboard,
    to: "/$tenantSlug/customers/$customerId",
  },
  { value: "energy", labelKey: "customer.energyTab", icon: IconBolt, to: "/$tenantSlug/customers/$customerId/energy" },
  {
    value: "correspondence",
    labelKey: "customer.correspondenceTab",
    icon: IconMessages,
    to: "/$tenantSlug/customers/$customerId",
  },
];

const CustomerDetailLayout = () => {
  const { t } = useI18n("host");
  const { customerId, tenantSlug } = useParams({ from: "/$tenantSlug/customers/$customerId" });
  const matches = useMatches();
  const navigate = useNavigate();
  const activeTab = matches.some((match) => match.routeId === "/$tenantSlug/customers/$customerId/energy")
    ? "energy"
    : "overview";

  return (
    <>
      <CustomerDetailHeader customerId={customerId} />
      <Tabs
        value={activeTab}
        onChange={(value) => {
          const tab = customerDetailTabs.find((item) => item.value === value);
          if (tab)
            void (navigate as (options: unknown) => void)(
              tab.value === "correspondence"
                ? { to: "/$tenantSlug/inbox", params: { tenantSlug }, search: { customerId: String(customerId) } }
                : { to: tab.to, params: { tenantSlug, customerId } },
            );
        }}
      >
        <Tabs.List>
          {customerDetailTabs.map(({ value, labelKey, icon: Icon }) => (
            <Tabs.Tab key={value} value={value} leftSection={<Icon size={16} />}>
              {t(labelKey)}
            </Tabs.Tab>
          ))}
        </Tabs.List>
      </Tabs>
      <Outlet />
    </>
  );
};
export const Route = createFileRoute("/$tenantSlug/customers/$customerId")({
  params: {
    parse: ({ customerId }) => ({ customerId: Number(customerId) }),
    stringify: ({ customerId }) => ({ customerId: String(customerId) }),
  },
  loader: async ({ context: { queryClient }, params }) => {
    try {
      await queryClient.ensureQueryData(customerQueryOptions(params.customerId));
    } catch (error) {
      if (error instanceof NotFoundError) throw notFound();
      throw error;
    }
  },
  component: CustomerDetailLayout,
});

import { Tabs } from "@mantine/core";
import { IconBolt, IconLayoutDashboard } from "@tabler/icons-react";
import { createFileRoute, notFound, Outlet, useMatches, useNavigate, useParams } from "@tanstack/react-router";
import { customerQueryOptions, NotFoundError } from "@vantigo/customers-ui/api/customers";
import { CustomerDetailHeader } from "@vantigo/customers-ui/pages/customers.$customerId";

type CustomerDetailTab = {
  value: "overview" | "energy";
  label: string;
  icon: typeof IconLayoutDashboard;
  to: "/customers/$customerId" | "/customers/$customerId/energy";
};

export const customerDetailTabs: CustomerDetailTab[] = [
  { value: "overview", label: "Overview", icon: IconLayoutDashboard, to: "/customers/$customerId" },
  { value: "energy", label: "Energy", icon: IconBolt, to: "/customers/$customerId/energy" },
];

const CustomerDetailLayout = () => {
  const { customerId } = useParams({ from: "/customers/$customerId" });
  const matches = useMatches();
  const navigate = useNavigate();
  const activeTab = matches.some((match) => match.routeId === "/customers/$customerId/energy") ? "energy" : "overview";

  return (
    <>
      <CustomerDetailHeader customerId={customerId} />
      <Tabs
        value={activeTab}
        onChange={(value) => {
          const tab = customerDetailTabs.find((item) => item.value === value);
          if (tab) void navigate({ to: tab.to, params: { customerId } });
        }}
      >
        <Tabs.List>
          {customerDetailTabs.map(({ value, label, icon: Icon }) => (
            <Tabs.Tab key={value} value={value} leftSection={<Icon size={16} />}>
              {label}
            </Tabs.Tab>
          ))}
        </Tabs.List>
      </Tabs>
      <Outlet />
    </>
  );
};
export const Route = createFileRoute("/customers/$customerId")({
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

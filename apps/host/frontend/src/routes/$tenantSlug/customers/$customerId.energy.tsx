import { createFileRoute, useParams } from "@tanstack/react-router";
import { CustomerEnergyPanel } from "@vantigo/energy-ui";

const CustomerEnergyRoute = () => {
  const { customerId } = useParams({ from: "/$tenantSlug/customers/$customerId" });
  return <CustomerEnergyPanel customerId={customerId} />;
};

export const Route = createFileRoute("/$tenantSlug/customers/$customerId/energy")({
  component: CustomerEnergyRoute,
});

import { createFileRoute, useParams } from "@tanstack/react-router";
import { CustomerOverview } from "@vantigo/customers-ui/pages/customers.$customerId";

const CustomerOverviewRoute = () => {
  const { customerId } = useParams({ from: "/$tenantSlug/customers/$customerId" });
  return <CustomerOverview customerId={customerId} />;
};

export const Route = createFileRoute("/$tenantSlug/customers/$customerId/")({
  component: CustomerOverviewRoute,
});

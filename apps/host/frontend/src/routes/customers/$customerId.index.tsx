import { createFileRoute, useParams } from "@tanstack/react-router";
import { CustomerOverview } from "@vantigo/customers-ui/pages/customers.$customerId";

const CustomerOverviewRoute = () => {
  const { customerId } = useParams({ from: "/customers/$customerId" });
  return <CustomerOverview customerId={customerId} />;
};

export const Route = createFileRoute("/customers/$customerId/")({
  component: CustomerOverviewRoute,
});

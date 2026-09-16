import { createFileRoute, useParams } from "@tanstack/react-router";
import { CustomerEnergyPanel } from "@vantigo/energy-ui";
import { useI18n } from "@vantigo/frontend-shell";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

// Lives in the customers app but calls the energy API, so it gates on the
// energy module itself: a pasted link must not hit an API 404.
const CustomerEnergyRoute = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  if (!enabledModuleKeys().includes("energy")) return <ModuleNotEnabledPage appLabel={t("navigation.energy")} />;
  return <CustomerEnergyPanel customerId={customerId} />;
};

export const Route = createFileRoute("/customers/$customerId/energy")({
  component: CustomerEnergyRoute,
});

import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { CustomerEnergyPanel } from "@vantigo/energy-ui";
import { useI18n } from "@vantigo/frontend-shell";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

/**
 * The customer page's Energy tab. It lives in the customers app but calls the
 * energy API, so it gates on the energy module itself: a pasted link must not
 * hit an API 404. Whether this caller may attach a metering point from it is
 * the host's answer, so it is passed in: not on a merged-away customer
 * (customers merge design D4), whose page is read-only — everything it had
 * moved to the customer it went into, and a supply period attached here
 * afterwards would never follow.
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const CustomerEnergyTab = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The layout's loader has already put the customer in the cache.
  const customer = useQuery(customerQueryOptions(customerId));
  if (!enabledModuleKeys().includes("energy")) return <ModuleNotEnabledPage appLabel={t("navigation.energy")} />;
  return <CustomerEnergyPanel customerId={customerId} canAttach={!customer.data?.mergedInto} />;
};

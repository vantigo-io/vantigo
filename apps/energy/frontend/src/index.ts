import type { AssistTarget } from "@vantigo/frontend-shell";
import targets from "./assist-targets.json";

export * from "./api/customers";
export * from "./api/energy";
export * from "./api/spot-prices";
export * from "./api/tariffs";
export { energyCatalog } from "./i18n";
export { CustomerEnergyPanel } from "./pages/customers.$customerId.energy";
export { MeteringPointDetailsPage } from "./pages/metering-points.$meteringPointId";
export { MeteringPointsPage } from "./pages/metering-points.index";
export { PricesPage } from "./pages/prices";
export { TariffsPage } from "./pages/tariffs";

/** The controls the Assistant can point at in a tour; each carries its key as a data-assist attribute. */
export const assistTargets: AssistTarget[] = targets;

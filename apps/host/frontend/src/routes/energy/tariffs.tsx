import { createFileRoute } from "@tanstack/react-router";
import { TariffsRoutePage } from "./-tariffs-page";

/** The Tariffs page (energy consumption billing design D11), behind energy:tariffs-view in the navigation. */
export const Route = createFileRoute("/energy/tariffs")({
  component: TariffsRoutePage,
});

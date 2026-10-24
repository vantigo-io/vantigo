import { createFileRoute } from "@tanstack/react-router";
import { PricesPage } from "./-prices-page";

export const Route = createFileRoute("/energy/prices")({
  component: PricesPage,
});

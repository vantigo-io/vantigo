import { createFileRoute } from "@tanstack/react-router";
import { ShopFloorPage } from "@vantigo/manufacturing-ui/pages/shop-floor";

/** The shop floor's work center picker (manufacturing design M31). */
export const Route = createFileRoute("/manufacturing/shop-floor/")({
  component: ShopFloorPage,
});

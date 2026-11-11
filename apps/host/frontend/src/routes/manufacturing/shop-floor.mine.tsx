import { createFileRoute } from "@tanstack/react-router";
import { MyWorkPage } from "@vantigo/manufacturing-ui/pages/my-work";

/** My work on the shop floor: the caller's open log and today's work orders (manufacturing design M31). */
export const Route = createFileRoute("/manufacturing/shop-floor/mine")({
  component: MyWorkPage,
});

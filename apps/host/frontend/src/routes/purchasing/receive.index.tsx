import { createFileRoute } from "@tanstack/react-router";
import { ReceivingRoutePage } from "./-purchasing-pages";

/**
 * The orders to receive (P30, A47): purchasing:receive's own list, which the
 * sidebar's Receive opens for a holder of that key alone.
 */
export const Route = createFileRoute("/purchasing/receive/")({
  component: ReceivingRoutePage,
});

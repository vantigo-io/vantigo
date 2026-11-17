import { createFileRoute } from "@tanstack/react-router";
import { PurchasingHomeRoutePage } from "./-purchasing-pages";

// The Purchasing app prefix sends a caller on: to the purchase orders, its
// home (purchasing design P23), or — for a receiver who holds
// purchasing:receive without purchasing:access — to the orders to receive
// (P30, A47). Which it is, the caller's permissions say, so it is decided
// once the session has answered rather than in beforeLoad.
export const Route = createFileRoute("/purchasing/")({
  component: PurchasingHomeRoutePage,
});

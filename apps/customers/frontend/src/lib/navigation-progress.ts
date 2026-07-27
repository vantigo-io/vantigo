import { nprogress } from "@mantine/nprogress";
import type { AnyRouter } from "@tanstack/react-router";

/**
 * Drives the navigation progress bar from the router's lifecycle: it starts when a
 * navigation begins loading and completes once all route loaders have settled
 * (including error and not-found outcomes).
 */
export const wireNavigationProgress = (router: AnyRouter) => {
  router.subscribe("onBeforeLoad", () => nprogress.start());
  router.subscribe("onResolved", () => nprogress.complete());
};

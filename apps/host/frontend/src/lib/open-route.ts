import { useRouter } from "@tanstack/react-router";
import { useCallback } from "react";

/**
 * Opens a route of the app the way a click on a link would: through the
 * router, so the page changes without a reload and anything mounted in the
 * layout (the assistant's panel) stays open. It is for the routes the assistant
 * writes into its answers (`/customers/42`, `/energy/metering-points/7?x=1`);
 * anything that is not a plain path of this app is ignored.
 */
export const useOpenRoute = () => {
  const router = useRouter();
  return useCallback(
    (href: string) => {
      if (!href.startsWith("/") || href.startsWith("//")) return;
      router.history.push(href);
    },
    [router],
  );
};

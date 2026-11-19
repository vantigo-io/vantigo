import { useQuery } from "@tanstack/react-query";
import { BrandProvider } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { appPathname, sessionlessPaths } from "../lib/public-paths";

/**
 * Gives the app its brand (brands design B13, B17): the signed-in person's own once the session is known,
 * the public one before and without it. The pay page, the RSVP page and a station ask for no session at
 * all (the public host's allowlist), so they ask for no brand either and keep what the stylesheet painted,
 * the default brand, until the invoice's own brand reaches the pay page.
 */
export const HostBrandProvider = ({ children }: { children: ReactNode }) => {
  const sessionless = sessionlessPaths.has(appPathname(window.location.pathname));
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, enabled: !sessionless });
  const userId = sessionless || session.isPending ? undefined : (session.data?.user.id ?? null);
  return (
    <BrandProvider userId={userId} sessionless={sessionless}>
      {children}
    </BrandProvider>
  );
};

import { useEffect, useRef } from "react";
import { useLiveTopic, useLiveUserId } from "../live";
import { refreshBrand } from "./provider";

/** The live topic of workspace-wide brand changes, and the prefix of a person's own (brands design B18). */
export const BRAND_TOPIC = "identity.brand";

/** How long, at most, a session waits before it asks for its brand again after a workspace-wide change (B18). */
export const BRAND_REFETCH_JITTER_MS = 5000;

export interface BrandLiveSyncProps {
  /** The longest wait before the refetch of a workspace-wide change. Test seam. */
  jitterMs?: number;
  /** Test seam: a number in [0, 1). */
  random?: () => number;
}

/**
 * Repaints an open session when its brand may have changed (brands design B18). Mount it once, under
 * the live provider. A workspace-wide event (a publish, a new default, an archive, the rules) makes
 * the session ask for its brand again after a random wait of up to five seconds, so a publish on a
 * thousand open sessions is not a thousand requests at once; any number of events within the wait
 * is one request. The person's own switch, heard by their other sessions, is asked for at once.
 * The first confirmation of a subscription is not a change; a reconnect's is, because events may
 * have been missed.
 */
export function BrandLiveSync({ jitterMs = BRAND_REFETCH_JITTER_MS, random = Math.random }: BrandLiveSyncProps) {
  const userId = useLiveUserId();
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const confirmed = useRef({ workspace: false, personal: false });

  useEffect(
    () => () => {
      if (timer.current !== undefined) clearTimeout(timer.current);
    },
    [],
  );

  useLiveTopic(BRAND_TOPIC, (event) => {
    if (event.kind === "resync" && !confirmed.current.workspace) {
      confirmed.current.workspace = true;
      return;
    }
    if (timer.current !== undefined) return;
    timer.current = setTimeout(
      () => {
        timer.current = undefined;
        refreshBrand();
      },
      Math.floor(random() * jitterMs),
    );
  });

  useLiveTopic(userId ? `${BRAND_TOPIC}:${userId}` : null, (event) => {
    if (event.kind === "resync" && !confirmed.current.personal) {
      confirmed.current.personal = true;
      return;
    }
    refreshBrand();
  });

  return null;
}

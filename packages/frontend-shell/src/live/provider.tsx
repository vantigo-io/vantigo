import { createContext, type ReactNode, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import {
  defaultLiveUrl,
  LiveClient,
  type LiveClientOptions,
  type LiveEvent,
  type LiveStatus,
  type PresenceMember,
  type PresenceState,
} from "./client";

const LiveContext = createContext<LiveClient | null>(null);

export type LiveProviderProps = {
  /** The signed-in person's id. A new id (signing in as someone else in the tab) opens a new socket. */
  userId: string;
  /** Test seam: the socket's address. */
  url?: LiveClientOptions["url"];
  /** Test seam: builds the socket. */
  createSocket?: LiveClientOptions["createSocket"];
  children: ReactNode;
};

/**
 * Owns the tab's one live socket. Mount it once, under the signed-in layout; a screen outside it
 * (or in a test) simply never hears events and sees the status `unavailable`.
 */
export function LiveProvider({ userId, url, createSocket, children }: LiveProviderProps) {
  const client = useMemo(
    () => new LiveClient({ userId, url: url ?? defaultLiveUrl(), createSocket }),
    [userId, url, createSocket],
  );
  useEffect(() => {
    client.start();
    return () => client.stop();
  }, [client]);
  return <LiveContext.Provider value={client}>{children}</LiveContext.Provider>;
}

const noop = () => {};
const noopSubscribe = () => noop;
const NO_MEMBERS: readonly PresenceMember[] = [];

/** The tab's live client, or null outside a provider: for a screen that sends binary frames (K34). */
export function useLiveClient(): LiveClient | null {
  return useContext(LiveContext);
}

/** `connecting | live | degraded | unavailable`. Only `live` means a screen may stop its fallback polling. */
export function useLiveStatus(): LiveStatus {
  const client = useContext(LiveContext);
  return useSyncExternalStore(
    client ? client.subscribeStatus : noopSubscribe,
    client ? client.getStatus : () => "unavailable",
  );
}

/**
 * Listens to a topic while mounted and `enabled` (default true). Components on one topic share
 * one subscription. `onEvent` gets a `LiveEvent`: `{ kind, ids }` per kind with the distinct ids
 * heard in a 250 ms trailing window, or `{ kind: "resync" }` once the subscription is confirmed
 * (and after every reconnect) and after a server resync. It need not be memoised.
 *
 * Returns `live`: true only while the socket is live AND the server answered `ok` for this topic;
 * false while pending, denied, unavailable, disabled or outside a provider. A screen polls as a
 * fallback exactly while this is false.
 */
export function useLiveTopic(
  topic: string | null | undefined,
  onEvent: (event: LiveEvent) => void,
  options: { enabled?: boolean } = {},
): boolean {
  const client = useContext(LiveContext);
  const latest = useRef(onEvent);
  latest.current = onEvent;
  const enabled = options.enabled ?? true;
  useEffect(() => {
    if (!client || !topic || !enabled) return;
    return client.listen(topic, (event) => latest.current(event));
  }, [client, topic, enabled]);
  return useSyncExternalStore(
    client && topic && enabled ? client.subscribeLive : noopSubscribe,
    client && topic && enabled ? () => client.isTopicLive(topic) : () => false,
  );
}

/**
 * The signed-in person's id the live provider was mounted with, or null outside a provider: what
 * a per-person topic (`calendar.mailbox-sync:<id>`) is keyed by.
 */
export function useLiveUserId(): string | null {
  const client = useContext(LiveContext);
  return client ? client.getUserId() : null;
}

/**
 * Announces `state` on a topic while mounted (repeated every 20 s, withdrawn on unmount) and
 * returns the others present: one entry per person, the strongest state, without the signed-in
 * person. Holds its own subscription to the topic. A `null` state or topic only watches / does nothing.
 */
export function usePresence(topic: string | null | undefined, state: PresenceState | null): readonly PresenceMember[] {
  const client = useContext(LiveContext);
  // Declared first so that on unmount the withdrawal is sent before the unsubscribe.
  useEffect(() => {
    if (!client || !topic || !state) return;
    return client.announce(topic, state);
  }, [client, topic, state]);
  useEffect(() => {
    if (!client || !topic) return;
    return client.listen(topic, noop);
  }, [client, topic]);
  return useSyncExternalStore(
    client && topic ? (listener) => client.subscribeMembers(topic, listener) : noopSubscribe,
    client && topic ? () => client.getMembers(topic) : () => NO_MEMBERS,
  );
}

import { type LiveClient, useLiveClient } from "@vantigo/frontend-shell";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { type CollabOptions, CollabProvider, type CollabState } from "./provider";

const OFFLINE: CollabState = {
  status: "offline",
  hasLog: false,
  paused: false,
  readOnly: false,
  failure: null,
  unsent: false,
};
const noop = () => {};

/** The document topic of an article (K26). */
export const docTopic = (articleId: string) => `knowledge.doc:${articleId}`;

/**
 * The page's collaborative provider while `enabled`: made when the article's live topic first
 * answers, kept through a lost connection (the document is typed in meanwhile, K33) and
 * replaced when the log is reset (`onReset`, K32). Null when there is no live socket or the
 * page never went live; then the editor is the single editor of K-A.
 */
export const useCollabProvider = ({
  articleId,
  enabled,
  canWrite,
  recheck,
  loadSource,
  buildSeed,
  onReset,
}: {
  articleId: string;
  enabled: boolean;
  canWrite: boolean;
  /** Changes when the page's access was read again: a server refusal of writes is then tried once more. */
  recheck: unknown;
  loadSource: CollabOptions["loadSource"];
  buildSeed: CollabOptions["buildSeed"];
  onReset: CollabOptions["onReset"];
}): { provider: CollabProvider | null; state: CollabState } => {
  const client = useLiveClient();
  const [provider, setProvider] = useState<CollabProvider | null>(null);
  // Read when the provider is made and when it calls back; a new function each render must not remake it.
  const latest = useRef({ loadSource, buildSeed, onReset, canWrite });
  useEffect(() => {
    latest.current = { loadSource, buildSeed, onReset, canWrite };
  });
  const [generation, setGeneration] = useState(0);
  const wanted = Boolean(client) && (enabled || provider !== null);

  useEffect(() => {
    if (!client || !wanted) return;
    const made = new CollabProvider({
      topic: docTopic(articleId),
      channel: client as LiveClient,
      canWrite: latest.current.canWrite,
      loadSource: () => latest.current.loadSource(),
      buildSeed: (content) => latest.current.buildSeed(content),
      onReset: (info) => {
        latest.current.onReset(info);
        setGeneration((value) => value + 1);
      },
    });
    setProvider(made);
    const leave = () => made.leave();
    const back = (event: PageTransitionEvent) => {
      if (event.persisted) made.rejoin();
    };
    // Text the server has not acknowledged is lost with the tab: ask first.
    const warn = (event: BeforeUnloadEvent) => {
      if (made.getState().unsent) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("pagehide", leave);
    window.addEventListener("pageshow", back);
    window.addEventListener("beforeunload", warn);
    return () => {
      window.removeEventListener("pagehide", leave);
      window.removeEventListener("pageshow", back);
      window.removeEventListener("beforeunload", warn);
      made.destroy();
      setProvider((current) => (current === made ? null : current));
    };
  }, [client, wanted, articleId, generation, latest]);

  useEffect(() => {
    provider?.setCanWrite(canWrite);
  }, [provider, canWrite, recheck]);

  const state = useSyncExternalStore(
    provider ? provider.subscribe : () => noop,
    provider ? provider.getState : () => OFFLINE,
  );
  return { provider, state };
};

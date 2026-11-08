import { useQueryClient } from "@tanstack/react-query";
import { PROFILE_QUERY_KEY, useLiveClient } from "@vantigo/frontend-shell";
import { useMemo, useSyncExternalStore } from "react";

/**
 * Twelve colours a name can be written on in white at AA contrast (knowledge design K30). A
 * person's colour is the same on every screen and every day: it comes from their id.
 */
export const CURSOR_COLOURS = [
  "#C2410C",
  "#B45309",
  "#4D7C0F",
  "#15803D",
  "#0F766E",
  "#0E7490",
  "#1D4ED8",
  "#4338CA",
  "#7E22CE",
  "#A21CAF",
  "#BE185D",
  "#BE123C",
] as const;

/** A stable colour for a user id: FNV-1a over its characters, modulo twelve. */
export const colourOf = (userId: string): string => {
  let hash = 0x811c9dc5;
  for (let index = 0; index < userId.length; index += 1) {
    hash ^= userId.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return CURSOR_COLOURS[hash % CURSOR_COLOURS.length];
};

/**
 * The signed-in person as the cursors show them: the live socket's user id and the host's profile
 * (read from its query cache under the shell's prefix, since the module does not import the host).
 * Null outside a live provider, where there is no co-editing either. The object is stable until
 * the name or id changes, so the editor is not remade, only its caret user updated.
 */
export const useCursorUser = (fallbackName: string): { id: string; name: string; color: string } | null => {
  const client = useLiveClient();
  const queryClient = useQueryClient();
  const cache = queryClient.getQueryCache();
  const userId = client?.userId ?? null;
  const profileName = useSyncExternalStore(
    (listener) => cache.subscribe(listener),
    () => {
      if (!userId) return "";
      const hit = queryClient
        .getQueriesData<{ displayName?: string }>({ queryKey: PROFILE_QUERY_KEY })
        .find(([key]) => key[2] === userId)?.[1];
      return hit?.displayName ?? "";
    },
  );
  return useMemo(
    () => (userId ? { id: userId, name: profileName || fallbackName, color: colourOf(userId) } : null),
    [userId, profileName, fallbackName],
  );
};

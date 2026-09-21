import type { QueryKey } from "@tanstack/react-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { invalidateCustomersExcept, syncCustomerRevision } from "../api/customers";

/**
 * The Reload behind the three modals that edit the customer row (the form
 * modal, contact info and the billing profile): after a revision conflict
 * (design D5) there is nothing to fix but look at what the server holds now,
 * and every one of them then has to re-seed its values *and* the revision its
 * next save sends — the half that actually unblocks that save.
 *
 * One fetch, and one only: `fetchQuery` puts the fresh row in the cache
 * itself, so the invalidation that follows deliberately skips the key just
 * fetched (`invalidateCustomersExcept`) instead of asking for it a second
 * time. Everything else under `["customers"]` was showing a row the server
 * has moved past — the list row this modal may have been opened from would
 * otherwise hand a stale revision to the *next* edit.
 *
 * A failed reload is the case worth spelling out. Awaiting
 * `invalidateQueries` would resolve even when the refetch underneath it
 * failed (query-core swallows the error), leaving the caller with stale
 * values, a stale revision and no conflict banner — a save that looks armed
 * and is not. So the fetch is awaited directly: on failure nothing is
 * touched, `failed` is raised for the modal to show, and the banner stays
 * exactly where it was.
 */
// A function declaration, not a generic arrow: every source file is parsed
// as TSX by the i18n source check, where `<T>(` reads as a JSX element.
export function useCustomerReload<T>({
  customerId,
  queryKey,
  fetchFresh,
  revisionOf,
  seed,
}: {
  customerId: number;
  /** The key `fetchFresh` fills, left out of the invalidation that follows — it holds fresh data by then. */
  queryKey: QueryKey;
  /** Fetches the row the server holds now, past the cache (`staleTime: 0`), and puts it in the cache. */
  fetchFresh: () => Promise<T>;
  /** The row revision the freshly fetched data carries — the customer's own, or the billing profile's, which is the same counter (design D4). */
  revisionOf: (fresh: T) => number | undefined;
  /** Re-seeds the modal's form values and its local revision from the fresh data. */
  seed: (fresh: T) => void;
}) {
  const queryClient = useQueryClient();
  const [reloading, setReloading] = useState(false);
  const [failed, setFailed] = useState(false);

  const reload = async () => {
    setReloading(true);
    setFailed(false);
    try {
      const fresh = await fetchFresh();
      seed(fresh);
      syncCustomerRevision(queryClient, customerId, revisionOf(fresh));
      invalidateCustomersExcept(queryClient, queryKey);
    } catch {
      setFailed(true);
    } finally {
      setReloading(false);
    }
  };

  /** Clears a previous failure — the modals call this as they (re)open, with their other per-open state. */
  const forget = () => setFailed(false);

  return { reload, reloading, failed, forget };
}

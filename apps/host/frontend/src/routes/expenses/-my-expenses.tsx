import { useQuery } from "@tanstack/react-query";
import { MyExpensesPage } from "@vantigo/expenses-ui/pages/my-expenses";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import "../../i18n";

/**
 * My expenses' entry point. `GET /entries` has no "mine" default — its scope
 * widens for `expenses:view-all`, `expenses:approve`, `expenses:manage` and a
 * project manager — so the page narrows it with the signed-in user's id,
 * which the host reads because it owns the session; no page in the package
 * does.
 *
 * The root layout has already put the session in the query cache before any
 * route under it renders (`__root.tsx`'s `beforeLoad`), so the skeleton below
 * is never actually seen in practice — it exists for the instant before that
 * fetch resolves on a cold cache, exactly as the project page's tabs guard
 * against the same gap.
 *
 * It sits beside the route file rather than inside it because a route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const MyExpenses = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  if (!session.data) return <ContentSkeleton rows={4} rowHeight={52} />;
  return <MyExpensesPage userId={session.data.user.id} />;
};

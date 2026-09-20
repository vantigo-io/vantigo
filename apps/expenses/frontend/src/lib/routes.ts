/**
 * The one path a travel claim lives at. The host owns the routes, but the
 * package's own pages link to a claim — "My expenses" from a row, and the
 * create modal the moment the trip exists — so the path is written once,
 * here, and the host's route file and this package's test route tree are both
 * built from it. A link that does not match a mounted route is a runtime
 * error in TanStack Router, and this is what keeps them from drifting apart.
 */
export const CLAIM_ROUTE_PATH = "/expenses/claims/$claimId";

/** What `navigate` and `Link` take to reach one travel claim. */
export const claimLinkOptions = (claimId: number) => ({
  to: CLAIM_ROUTE_PATH,
  params: { claimId: String(claimId) },
});

/**
 * The same path as a plain URL, for a component the host may mount **outside**
 * the expenses route tree — the project page's Expenses tab, which renders its
 * links through the shell's link component rather than this package's router
 * hooks. Built from the one constant above, so a renamed route cannot leave a
 * dead link behind on the project page.
 */
export const claimHref = (claimId: number): string => CLAIM_ROUTE_PATH.replace("$claimId", String(claimId));

/**
 * The label for one typed contact role (typed contact roles design D2), in this
 * package's own catalog — the same shape `addressTypeLabel` has, including the
 * fallback: the role vocabulary is a value change on the server rather than a
 * migration, so a widened list can reach a frontend whose catalog predates it,
 * and showing the code is the honest answer to that.
 */
export const contactRoleLabel = (t: (key: string) => string, role: string): string =>
  role === "billing"
    ? t("roleBilling")
    : role === "project"
      ? t("roleProject")
      : role === "decision_maker"
        ? t("roleDecisionMaker")
        : role;

/**
 * The "Primary … contact" sentence for one role (the badge's `aria-label`/
 * tooltip and the Primary switch's `aria-label` both use it). For the three
 * known roles this reads a whole composed catalog key rather than
 * interpolating `contactRoleLabel` into a template: nb compounds close up
 * ("fakturakontakt", not "Faktura-kontakt"), and a template cannot produce a
 * closed compound from an interpolated noun. A role the catalog does not know
 * falls back to the interpolated template, which is the best either language
 * can honestly do for a name it has never seen.
 */
export const primaryContactLabel = (
  t: (key: string, options?: Record<string, unknown>) => string,
  role: string,
): string =>
  role === "billing"
    ? t("primaryRoleForBilling")
    : role === "project"
      ? t("primaryRoleForProject")
      : role === "decision_maker"
        ? t("primaryRoleForDecisionMaker")
        : t("primaryRoleFor", { role: contactRoleLabel(t, role).toLowerCase() });

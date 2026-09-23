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

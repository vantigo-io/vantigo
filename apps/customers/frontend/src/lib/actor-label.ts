/**
 * The label a timeline entry, or one of its revisions, shows for its author
 * (design D1).
 *
 * `actorKind` decides it, not the snapshotted name, because the server's
 * "nobody in particular" cases are English literals it stored at write time —
 * `"System"` for an event the module generated, `"Unattributed"` for a manual
 * write with no user principal, `"Unknown user"` for a writer the directory no
 * longer knows about — and a Norwegian reader must not be shown them. Anything
 * else is a real person's name, which is never translated.
 *
 * `"Unknown user"` is matched on the display rather than the kind because the
 * server files it under kind `user`: there *is* a user id, only no account left
 * to name it with.
 */
export const actorLabel = (
  actorKind: string | null | undefined,
  actorDisplay: string | null | undefined,
  t: (key: string) => string,
): string => {
  if (actorKind === "system") return t("actorSystem");
  if (actorKind === "unattributed") return t("unattributed");
  const display = actorDisplay?.trim();
  if (!display) return t("unattributed");
  return display === "Unknown user" ? t("actorUnknownUser") : display;
};

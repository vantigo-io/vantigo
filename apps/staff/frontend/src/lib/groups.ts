import type { SupportedLocale } from "@vantigo/frontend-shell";
import type { StaffGroupProvider, StaffGroupSyncState, StaffGroupTraitText } from "../api/groups";
import type { StaffMeta } from "../api/meta";
import { staffCatalog } from "../i18n";

type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * A provider's words — a trait's label, a description, a choice — in the
 * reader's language, with English as the fallback: the declaration is the
 * provider's, and only `en` is promised.
 */
export const traitText = (text: StaffGroupTraitText | undefined, locale: SupportedLocale): string =>
  text ? (locale === "nb" ? (text.nb ?? text.en) : text.en) : "";

/** A provider's display name as `/meta` lists it, else its key. */
export const providerNameOf = (key: string, providers: StaffMeta["providers"]): string =>
  providers?.find((provider) => provider.key === key)?.name ?? key;

/** The colour of a section's sync state, the same on the list and the group page. */
export const syncStateColor: Record<StaffGroupSyncState, string> = {
  pending: "blue",
  synced: "teal",
  failed: "red",
  retired: "gray",
  deleted: "gray",
};

/**
 * Whether the provider found a group in its directory, adopted it and waits
 * for a holder of its permission to confirm it there: the section stays
 * pending with `group_unconfirmed`, which is no failure.
 */
export const awaitsConfirmation = (provider: Pick<StaffGroupProvider, "state" | "lastError">): boolean =>
  provider.state === "pending" && provider.lastError === "group_unconfirmed";

/**
 * Whether the group may be deleted rather than archived, as the server
 * judges it (`group_synced` otherwise): no provider has synced it, holds it
 * by an external id, or is turned on for it. A sync in flight, which the page
 * cannot see, the server still refuses.
 */
export const groupDeletable = (providers: Pick<StaffGroupProvider, "syncedAt" | "externalId" | "enabled">[]): boolean =>
  !providers.some(
    (provider) => provider.syncedAt !== undefined || provider.externalId !== undefined || provider.enabled,
  );

/**
 * A section's `lastError` in words: a trait's code, `group_not_manageable`,
 * `group_unconfirmed`, `call_failed`, or `members_failed:<n>` with its count.
 * A code a later server writes is still said, by its code.
 */
export const groupErrorWords = (t: Translate, code: string): string => {
  const [bare, detail] = code.split(/:(.*)/s);
  if (bare === "members_failed") return t("groupError_members_failed", { count: Number(detail) || 0 });
  return `groupError_${bare}` in staffCatalog.en ? t(`groupError_${bare}`) : t("groupErrorUnknown", { code });
};

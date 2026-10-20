import type { SupportedLocale } from "@vantigo/frontend-shell";
import type { StaffGroupSyncState, StaffGroupTraitText } from "../api/groups";
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
 * A section's `lastError` in words: a trait's code, `group_not_manageable`,
 * `call_failed`, or `members_failed:<n>` with its count. A code a later
 * server writes is still said, by its code.
 */
export const groupErrorWords = (t: Translate, code: string): string => {
  const [bare, detail] = code.split(/:(.*)/s);
  if (bare === "members_failed") return t("groupError_members_failed", { count: Number(detail) || 0 });
  return `groupError_${bare}` in staffCatalog.en ? t(`groupError_${bare}`) : t("groupErrorUnknown", { code });
};

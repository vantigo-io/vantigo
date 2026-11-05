import type { ChannelProviderKey } from "../api/channels";

/**
 * Whether a provider key names a mailbox provider rather than SMTP (Google Workspace design G4): the
 * module names no mailbox provider, so anything GET /channel-providers lists beside SMTP is one.
 */
export const isMailboxProvider = (provider: ChannelProviderKey): boolean => provider !== "smtp";

/**
 * The texts that differ by mailbox provider, by the catalog key of Microsoft 365's (the text the channels
 * had before the providers became many): Google Workspace's own key for each.
 */
const googleTexts: Record<string, string> = {
  tenantWide: "tenantWideGoogle",
  sharedMailboxOnly: "sharedMailboxOnlyGoogle",
  errorPersonalMailbox: "errorPersonalMailboxGoogle",
  errorMailboxDenied: "errorMailboxDeniedGoogle",
  errorMailboxDeniedLink: "errorMailboxDeniedLinkGoogle",
  "syncError.mailbox_denied": "syncError.google_mailbox_denied",
};

/** The catalog key of `key`'s text for the provider: Google Workspace's own where it has one, else `key`. */
export const providerText = (key: string, provider: string): string =>
  (provider === "google" && googleTexts[key]) || key;

/** The administration guide's mail channels section of the provider's page, as a site path below the locale. */
export const mailChannelsDocs = (provider: string): string =>
  provider === "google" ? "/admin/google-workspace/#mail-channels" : "/admin/entra/#mail-channels";

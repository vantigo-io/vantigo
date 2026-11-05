import type { Channel, ChannelProvider, ChannelProviderKey } from "../api/channels";

/**
 * Whether a provider key names a mailbox provider rather than SMTP (Google Workspace design G4): the
 * module names no mailbox provider, so anything GET /channel-providers lists beside SMTP is one — of an
 * e-mail channel. A Messenger channel's provider is told by the channel's type (isMailboxChannel) and the
 * provider list by its own (mailboxProviders).
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

/** Whether a channel is a Messenger chat; every channel from before Messenger is e-mail. */
export const isMessengerChannel = (channel: Pick<Channel, "type">): boolean => channel.type === "messenger";

/** Whether a channel is a mailbox channel: an e-mail channel on a provider other than SMTP. */
export const isMailboxChannel = (channel: Pick<Channel, "type" | "provider">): boolean =>
  !isMessengerChannel(channel) && isMailboxProvider(channel.provider);

/** The providers of e-mail channels beside SMTP, as the server lists them. */
export const mailboxProviders = (providers: ChannelProvider[]): ChannelProvider[] =>
  providers.filter((provider) => provider.type !== "messenger" && isMailboxProvider(provider.key));

/** The providers of Messenger channels. */
export const messengerProviders = (providers: ChannelProvider[]): ChannelProvider[] =>
  providers.filter((provider) => provider.type === "messenger");

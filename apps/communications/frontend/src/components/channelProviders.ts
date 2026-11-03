import type { ChannelProviderKey } from "../api/channels";

/**
 * Whether a provider key names a mailbox provider rather than SMTP (Google Workspace design G4): the
 * module names no mailbox provider, so anything GET /channel-providers lists beside SMTP is one.
 */
export const isMailboxProvider = (provider: ChannelProviderKey): boolean => provider !== "smtp";

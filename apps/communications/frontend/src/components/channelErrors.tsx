import { Anchor } from "@mantine/core";
import { appUrl, type useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { mailChannelsDocs, providerText } from "./channelProviders";

type Translate = ReturnType<typeof useI18n>["t"];

const codeOf = (error: unknown) =>
  error && typeof error === "object" && "code" in error && typeof error.code === "string" ? error.code : undefined;
const statusOf = (error: unknown) =>
  error && typeof error === "object" && "status" in error && typeof error.status === "number"
    ? error.status
    : undefined;

const messages: Record<string, string> = {
  personal_mailbox: "errorPersonalMailbox",
  group_address: "errorGroupAddress",
  provider_switch_unsupported: "errorProviderSwitchUnsupported",
  mailbox_unknown: "errorMailboxUnknown",
  mail_channels_off: "errorMailChannelsOff",
  mailbox_exists: "errorMailboxExists",
  channel_exists: "errorAddressExists",
  provider_unreachable: "errorProviderUnreachable",
  provider_unchanged: "errorProviderUnchanged",
  mailbox_changed: "errorMailboxChanged",
  verification_failed: "errorVerificationFailed",
  destination_rejected: "errorDestinationRejected",
  default_channel_conflict: "errorDefaultChannelConflict",
  provider_type_mismatch: "errorProviderTypeMismatch",
  messenger_off: "errorMessengerOff",
  endpoint_unknown: "errorEndpointUnknown",
  endpoint_unavailable: "errorEndpointUnavailable",
};

/**
 * A channel request's refusal in the reader's language, by the server's code:
 * never the code itself, nor the server's English message. `channel_exists`
 * is an address another channel has, `mailbox_exists` a mailbox another
 * channel on the same provider uses. `provider` is the mailbox provider's
 * name as GET /channel-providers answers it, for the refusals that name it,
 * and `providerKey` its key, for the refusals whose text and help link are
 * the provider's own (providerText, mailChannelsDocs).
 */
export function channelErrorMessage(
  error: unknown,
  t: Translate,
  locale: string,
  fallback = "errorChannelNotSaved",
  provider = "",
  providerKey = "",
): ReactNode {
  const code = codeOf(error);
  if (statusOf(error) === 429) return t("errorRateLimited");
  if (code === "mailbox_denied")
    return (
      <>
        <span>{t(providerText("errorMailboxDenied", providerKey), { provider })}</span>{" "}
        <Anchor
          href={appUrl(`/docs/${locale}${mailChannelsDocs(providerKey)}`)}
          target="_blank"
          rel="noopener noreferrer"
        >
          {t(providerText("errorMailboxDeniedLink", providerKey), { provider })}
        </Anchor>
      </>
    );
  const key = code ? messages[code] : undefined;
  return t(key ? providerText(key, providerKey) : fallback, { provider });
}

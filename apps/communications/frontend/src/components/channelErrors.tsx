import { Anchor } from "@mantine/core";
import { appUrl, type useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";

type Translate = ReturnType<typeof useI18n>["t"];

const codeOf = (error: unknown) =>
  error && typeof error === "object" && "code" in error && typeof error.code === "string" ? error.code : undefined;
const statusOf = (error: unknown) =>
  error && typeof error === "object" && "status" in error && typeof error.status === "number"
    ? error.status
    : undefined;

const messages: Record<string, string> = {
  personal_mailbox: "errorPersonalMailbox",
  mailbox_unknown: "errorMailboxUnknown",
  mail_channels_off: "errorMailChannelsOff",
  provider_unreachable: "errorProviderUnreachable",
  provider_unchanged: "errorProviderUnchanged",
  mailbox_changed: "errorMailboxChanged",
  verification_failed: "errorVerificationFailed",
  destination_rejected: "errorDestinationRejected",
  default_channel_conflict: "errorDefaultChannelConflict",
};

/**
 * A channel request's refusal in the reader's language, by the server's code:
 * never the code itself, nor the server's English message. `channel_exists`
 * names the mailbox for a Microsoft 365 channel and the address for SMTP.
 */
export function channelErrorMessage(
  error: unknown,
  t: Translate,
  provider: string,
  locale: string,
  fallback = "errorChannelNotSaved",
): ReactNode {
  const code = codeOf(error);
  if (statusOf(error) === 429) return t("errorRateLimited");
  if (code === "mailbox_denied")
    return (
      <>
        <span>{t("errorMailboxDenied")}</span>{" "}
        <Anchor href={appUrl(`/docs/${locale}/admin/entra/#mail-channels`)} target="_blank" rel="noopener noreferrer">
          {t("errorMailboxDeniedLink")}
        </Anchor>
      </>
    );
  if (code === "channel_exists") return provider === "entra" ? t("errorMailboxExists") : t("errorAddressExists");
  const key = code ? messages[code] : undefined;
  return t(key ?? fallback);
}

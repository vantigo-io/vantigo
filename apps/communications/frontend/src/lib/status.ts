import type { MessageStatus } from "../api/messages";

type Translate = (key: string) => string;

const labelKeys: Record<MessageStatus, string> = {
  queued: "statusQueued",
  relay_accepted: "statusRelayAccepted",
  submission_failed: "statusSubmissionFailed",
};
const extraLabelKeys: Record<string, string> = {
  sending: "statusSending",
  retrying: "statusRetrying",
  cancelled: "statusCancelled",
  suppressed: "statusSuppressed",
  message_queued: "eventMessageQueued",
};
export const statusLabel = (status: string, t: Translate) => {
  const key = labelKeys[status as MessageStatus] || extraLabelKeys[status];
  return key ? t(key) : status.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
};
export const statusColor = (status: string) =>
  ({ queued: "gray", relay_accepted: "teal", submission_failed: "red" })[status] || "gray";
export const eventLabel = (eventType: string, t: Translate) => statusLabel(eventType, t);

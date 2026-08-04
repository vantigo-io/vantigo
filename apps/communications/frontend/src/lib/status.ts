import type { MessageStatus } from "../api/messages";

const labels: Record<MessageStatus, string> = {
  queued: "Queued",
  relay_accepted: "Relay accepted",
  submission_failed: "Submission failed",
};
export const statusLabel = (status: string) =>
  labels[status as MessageStatus] ?? status.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
export const statusColor = (status: string) =>
  ({ queued: "gray", relay_accepted: "teal", submission_failed: "red" })[status] || "gray";
export const eventLabel = (eventType: string) => statusLabel(eventType);

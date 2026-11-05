import { Badge, Group, Text } from "@mantine/core";
import { IconCheck } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type Attachment, type ConversationMessage, fetchInlineImage, inlineImageTypes } from "../api/conversations";
import { AttachmentLinks } from "./AttachmentLinks";
import { ChatCarousel } from "./ChatCarousel";
import { participantName, relative } from "./conversationHelpers";

const isImage = (file: Attachment) =>
  file.role !== "card" &&
  file.downloadAvailable &&
  inlineImageTypes.includes(file.contentType.split(";")[0]?.trim().toLowerCase() ?? "");

/** An image of the chat shown inline, as a data: URI the page fetched — as e-mail's inline images are. */
function ChatImage({ file }: { file: Attachment }) {
  const image = useQuery({
    queryKey: ["chat-image", file.id],
    queryFn: ({ signal }) => fetchInlineImage(file.downloadPath, signal),
    staleTime: Number.POSITIVE_INFINITY,
  });
  // One that cannot be shown — too large, or not an allowed image — is still a file to download.
  if (image.data === null) return <AttachmentLinks attachments={[file]} />;
  if (!image.data) return <div className="chat-image placeholder" />;
  return <img className="chat-image" src={image.data} alt={file.fileName} />;
}

/** A delivery's status as the sender reads it; the codes of a failure have a sentence each. */
const statusKeys: Record<string, string> = {
  queued: "chatStatusQueued",
  sending: "chatStatusQueued",
  retrying: "chatStatusRetrying",
  relay_accepted: "chatStatusSent",
  delivered: "chatStatusDelivered",
  read: "chatStatusRead",
  submission_failed: "chatStatusFailed",
  suppressed: "chatStatusFailed",
  cancelled: "chatStatusFailed",
};
const failureKeys: Record<string, string> = {
  recipient_unreachable: "chatFailureRecipientUnreachable",
  reply_window_closed: "chatFailureWindowClosed",
  human_agent_not_permitted: "chatFailureHumanAgent",
  rejected: "chatFailureRejected",
  provider_unavailable: "chatFailureProviderUnavailable",
};

export type ChatMessageProps = {
  message: ConversationMessage;
  /** The whole thread: an option is marked chosen by the answer that names it. */
  messages: ConversationMessage[];
};

/**
 * One message of a Messenger conversation as a chat bubble (design N8): text, its images inline, its cards
 * as a carousel, its answer options as chips — the chosen one marked — and an answer as a chip with a
 * check ("✓ Yes") under its question. An echo of the Page's own, sent from elsewhere, says so.
 */
export function ChatMessage({ message, messages }: ChatMessageProps) {
  const { t } = useI18n("communications");
  const note = message.direction === "internal_note";
  const outbound = message.direction === "outbound";
  const answer = message.answerTo ?? null;
  const sharedEmail = answer?.choiceId === "share-email";
  const options = message.components?.answerOptions ?? [];
  const cards = message.components?.cards ?? [];
  const chosen = (optionId: string) =>
    messages.some((other) => other.answerTo?.messageId === message.id && other.answerTo.choiceId === optionId);
  const images = message.attachments.filter(isImage);
  const files = message.attachments.filter((file) => !isImage(file));
  const delivery = outbound && !message.echo ? message.deliveries[0] : undefined;
  const failure = delivery?.error ? failureKeys[delivery.error] : undefined;

  return (
    <article className={`chat-row ${message.direction}`}>
      <div className={`chat-bubble ${message.direction}`}>
        <Group justify="space-between" gap="md" wrap="nowrap">
          <Text size="xs" fw={700} c="dimmed">
            {note
              ? t("internalNote")
              : outbound
                ? t("you")
                : message.participant
                  ? participantName(message.participant)
                  : t("customer")}
          </Text>
          <Text size="xs" c="dimmed">
            {relative(message.occurredAt)}
          </Text>
        </Group>
        {answer && !sharedEmail ? (
          <Badge mt={4} size="lg" variant="light" leftSection={<IconCheck size={12} />}>
            {answer.title}
          </Badge>
        ) : (
          message.textBody && (
            <Text mt={4} style={{ whiteSpace: "pre-wrap" }}>
              {message.textBody}
            </Text>
          )
        )}
        {sharedEmail && (
          <Text size="xs" c="dimmed">
            {t("chatEmailShared")}
          </Text>
        )}
        {images.map((file) => (
          <ChatImage key={file.id} file={file} />
        ))}
        <AttachmentLinks attachments={files} />
        <ChatCarousel cards={cards} attachments={message.attachments} />
        {(options.length > 0 || message.components?.shareEmail) && (
          <Group gap={6} mt="xs">
            {options.map((option) => (
              <Badge
                key={option.id}
                variant={chosen(option.id) ? "filled" : "outline"}
                leftSection={chosen(option.id) ? <IconCheck size={12} /> : undefined}
              >
                {option.title}
              </Badge>
            ))}
            {message.components?.shareEmail && (
              <Badge variant="outline" color="gray">
                {t("chatAskedForEmail")}
              </Badge>
            )}
          </Group>
        )}
        {message.echo && (
          <Text size="xs" c="dimmed" mt={4}>
            {t("chatSentFromMeta")}
          </Text>
        )}
        {delivery && (
          <Text size="xs" c={delivery.status === "submission_failed" ? "red" : "dimmed"} mt={4}>
            {t(statusKeys[delivery.status] ?? "chatStatusQueued")}
            {failure ? ` · ${t(failure)}` : ""}
          </Text>
        )}
      </div>
    </article>
  );
}

import { ActionIcon, Badge, Button, Group, Menu, ScrollArea, Select, Text, Title } from "@mantine/core";
import {
  IconAlertTriangle,
  IconArchive,
  IconCheck,
  IconChevronDown,
  IconDots,
  IconDownload,
  IconPaperclip,
  IconSparkles,
  IconTag,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ConversationDetail, ConversationMessage, ConversationStatus, Tag } from "../api/conversations";
import { attachmentDownloadUrl, inlineImages, rawMessageDownloadUrl, safeHtmlSrcDoc } from "../api/conversations";
import { ConversationComposer } from "./ConversationComposer";
import { participantName, relative } from "./conversationHelpers";

export type ConversationThreadProps = {
  conversation: ConversationDetail;
  tags: Tag[];
  onRead: () => void;
  onWorkflow: (body: { status?: ConversationStatus; customerId?: number | null }) => void;
  onTag: (id: string, remove?: boolean) => void;
  onSent: () => Promise<unknown>;
  onSuggest: () => void;
  suggestionPending: boolean;
  suggestionError: Error | null;
};

export function ConversationThread({
  conversation,
  tags,
  onRead,
  onWorkflow,
  onTag,
  onSent,
  onSuggest,
  suggestionPending,
  suggestionError,
}: ConversationThreadProps) {
  const { t } = useI18n("communications");

  return (
    <div className="thread-layout">
      <header className="thread-header">
        <div>
          <Text size="xs" c="dimmed">
            {conversation.participants.map(participantName).join(", ")}
          </Text>
          <Title order={3}>{conversation.subject || "No subject"}</Title>
          <Group gap="xs" mt="xs">
            {conversation.customerId ? (
              <Badge color="teal">
                {t("customerAssociation", {
                  id: conversation.customerId,
                  source: conversation.customerAssociationSource || "associated",
                })}
              </Badge>
            ) : conversation.suggestedCustomerId ? (
              <Badge color="yellow">
                {t("suggestedCustomer", { id: conversation.suggestedCustomerId })}
                {conversation.suggestedCustomerConfidence != null
                  ? ` · ${Math.round(conversation.suggestedCustomerConfidence * 100)}%`
                  : ""}
              </Badge>
            ) : conversation.candidateCustomerIds.length > 0 ? (
              <Group gap="xs">
                <Badge color="yellow">
                  {t("customerCandidates", { count: conversation.candidateCustomerIds.length })}
                </Badge>
                <Button
                  size="xs"
                  variant="light"
                  loading={suggestionPending}
                  onClick={onSuggest}
                  leftSection={<IconSparkles size={13} />}
                >
                  {t("suggestCustomer")}
                </Button>
              </Group>
            ) : (
              <Text size="xs" c="dimmed">
                {t("noCustomerAssociation")}
              </Text>
            )}
          </Group>
          {conversation.suggestedCustomerId && !conversation.customerId && (
            <Group gap="xs" mt="xs">
              <Text size="xs" c="dimmed">
                {conversation.suggestedCustomerReasoning || t("aiSuggestion")}
              </Text>
              <Button size="xs" onClick={() => onWorkflow({ customerId: conversation.suggestedCustomerId })}>
                {t("confirmCustomer")}
              </Button>
            </Group>
          )}
          {suggestionError && (
            <Text size="xs" c="red">
              {suggestionError.message.includes("503") ? t("aiSuggestionsUnavailable") : suggestionError.message}
            </Text>
          )}
        </div>
        <Group>
          <Select
            aria-label={t("statusLabel")}
            value={conversation.status}
            data={["open", "closed", "archived"]}
            onChange={(value) => value && onWorkflow({ status: value as ConversationStatus })}
          />
          <Menu>
            <Menu.Target>
              <Button variant="light" rightSection={<IconChevronDown size={14} />}>
                {t("actions")}
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconCheck size={14} />} onClick={onRead}>
                {t("markRead")}
              </Menu.Item>
              <Menu.Item leftSection={<IconArchive size={14} />} onClick={() => onWorkflow({ status: "archived" })}>
                {t("archive")}
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </header>
      <div className="thread-tags">
        {conversation.tags.map((tag) => (
          <Badge
            key={tag.id}
            color={tag.color || "indigo"}
            rightSection={
              <button type="button" className="tag-remove" onClick={() => onTag(tag.id, true)}>
                ×
              </button>
            }
          >
            {tag.name}
          </Badge>
        ))}
        <Select
          size="xs"
          placeholder={t("addTag")}
          data={tags
            .filter((tag) => !conversation.tags.some((existing) => existing.id === tag.id))
            .map((tag) => ({ value: tag.id, label: tag.name }))}
          onChange={(value) => value && onTag(value)}
          leftSection={<IconTag size={13} />}
        />
      </div>
      <ScrollArea className="thread-messages">
        {conversation.messages.map((message) => (
          <MessageCard key={message.id} message={message} />
        ))}
      </ScrollArea>
      <ConversationComposer conversation={conversation} onSent={onSent} />
    </div>
  );
}

/**
 * One message: HTML whenever the message has it, in the sandboxed iframe with its own inline images
 * turned into data: URIs by the page, and Show as text beside it; the text otherwise. An unreadable
 * message says so and offers its original; an inbound one offers its original in a menu.
 */
function MessageCard({ message }: { message: ConversationMessage }) {
  const { t } = useI18n("communications");
  const [asText, setAsText] = useState(false);
  const html = message.unreadable ? null : message.htmlBody;
  const withImages = useQuery({
    queryKey: ["conversation-message-html", message.id],
    queryFn: () => inlineImages(html ?? "", message.attachments),
    enabled: Boolean(html) && !asText,
    staleTime: Number.POSITIVE_INFINITY,
  });
  const rawPath = message.rawDownloadPath ?? null;
  const unscanned = message.attachments.some((file) => file.downloadAvailable && file.scanStatus === "unscanned");

  return (
    <article className={`message-card ${message.direction}`}>
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs">
          <Text fw={700}>
            {message.direction === "internal_note"
              ? "Internal note"
              : message.direction === "outbound"
                ? "You"
                : message.participant
                  ? participantName(message.participant)
                  : "Customer"}
          </Text>
          {message.autoSubmitted && (
            <Badge size="sm" variant="light" color="gray">
              {t("autoReply")}
            </Badge>
          )}
        </Group>
        <Group gap="xs" wrap="nowrap">
          <Text size="xs" c="dimmed">
            {relative(message.occurredAt)}
          </Text>
          {html && (
            <Button size="compact-xs" variant="subtle" onClick={() => setAsText((value) => !value)}>
              {asText ? t("showFormatted") : t("showAsText")}
            </Button>
          )}
          {rawPath && (
            <Menu position="bottom-end">
              <Menu.Target>
                <ActionIcon size="sm" variant="subtle" color="gray" aria-label={t("messageActions")}>
                  <IconDots size={14} />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item
                  component="a"
                  href={rawMessageDownloadUrl(rawPath)}
                  download
                  leftSection={<IconDownload size={14} />}
                >
                  {t("downloadOriginal")}
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          )}
        </Group>
      </Group>
      {message.unreadable ? (
        <Group mt="xs" gap="sm">
          <Text c="dimmed">{t("unreadableMessage")}</Text>
          {rawPath && (
            <Button
              component="a"
              href={rawMessageDownloadUrl(rawPath)}
              download
              size="compact-xs"
              variant="light"
              leftSection={<IconDownload size={12} />}
            >
              {t("downloadOriginal")}
            </Button>
          )}
        </Group>
      ) : html && !asText ? (
        <iframe
          title={t("messageHtml")}
          sandbox=""
          srcDoc={safeHtmlSrcDoc(withImages.data ?? html)}
          className="message-html"
        />
      ) : (
        message.textBody && (
          <Text mt="xs" style={{ whiteSpace: "pre-wrap" }}>
            {message.textBody}
          </Text>
        )
      )}
      {message.attachments.length > 0 && (
        <Group mt="sm" gap="xs">
          {message.attachments.map((file) =>
            file.downloadAvailable && file.scanStatus === "clean" ? (
              <Button
                key={file.id}
                component="a"
                href={attachmentDownloadUrl(file.downloadPath)}
                target="_blank"
                rel="noreferrer"
                size="compact-xs"
                variant="subtle"
                leftSection={<IconPaperclip size={12} />}
              >
                {file.fileName}
              </Button>
            ) : file.downloadAvailable && file.scanStatus === "unscanned" ? (
              <Button
                key={file.id}
                component="a"
                href={attachmentDownloadUrl(file.downloadPath)}
                download={file.fileName}
                size="compact-xs"
                variant="subtle"
                color="orange"
                title={t("attachmentNotScanned")}
                leftSection={<IconAlertTriangle size={12} />}
              >
                {file.fileName}
              </Button>
            ) : (
              <Badge key={file.id} leftSection={<IconPaperclip size={12} />} variant="outline">
                {file.scanStatus === "pending" ? t("scanning") : t("attachmentUnavailable")}
              </Badge>
            ),
          )}
        </Group>
      )}
      {unscanned && (
        <Text size="xs" c="orange" mt={4}>
          {t("attachmentNotScanned")}
        </Text>
      )}
    </article>
  );
}

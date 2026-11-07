import {
  ActionIcon,
  Alert,
  Avatar,
  Badge,
  Button,
  Group,
  Menu,
  ScrollArea,
  Select,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
  IconArchive,
  IconCheck,
  IconChevronDown,
  IconDots,
  IconDownload,
  IconSparkles,
  IconTag,
} from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { type PresenceMember, useI18n, usePresence } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useState } from "react";
import type { ConversationDetail, ConversationMessage, ConversationStatus, Tag } from "../api/conversations";
import { inlineImages, rawMessageDownloadUrl, retryMessage, safeHtmlSrcDoc } from "../api/conversations";
import { AttachmentLinks } from "./AttachmentLinks";
import { ChatMessage } from "./ChatMessage";
import { type ConnectionButtonProps, ConversationComposer } from "./ConversationComposer";
import { isChat } from "./chat";
import { participantName, relative, returnHere } from "./conversationHelpers";
import { MessengerContactLink } from "./MessengerContactLink";

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
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
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
  connectionButton,
}: ConversationThreadProps) {
  const { t } = useI18n("communications");
  const [drafting, setDrafting] = useState(false);
  const others = usePresence(`communications.conversation:${conversation.id}`, drafting ? "replying" : "viewing");

  return (
    <div className="thread-layout">
      <header className="thread-header">
        <div>
          <Text size="xs" c="dimmed">
            {conversation.participants.map(participantName).join(", ")}
          </Text>
          <Title order={3}>
            {isChat(conversation)
              ? conversation.participants[0]
                ? participantName(conversation.participants[0])
                : t("customer")
              : conversation.subject || "No subject"}
          </Title>
          <PresenceLine members={others} />
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
          {conversation.messengerLink && (
            <MessengerContactLink
              conversationId={conversation.id}
              customerId={conversation.customerId}
              link={conversation.messengerLink}
            />
          )}
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
        {conversation.messages.map((message) =>
          isChat(conversation) ? (
            <ChatMessage
              key={message.id}
              message={message}
              messages={conversation.messages}
              customerId={conversation.customerId}
            />
          ) : (
            <MessageCard
              key={message.id}
              message={message}
              conversation={conversation}
              onSent={onSent}
              connectionButton={connectionButton}
            />
          ),
        )}
      </ScrollArea>
      <ConversationComposer
        conversation={conversation}
        onSent={onSent}
        connectionButton={connectionButton}
        onDraftingChange={setDrafting}
      />
    </div>
  );
}

/**
 * Who else is in the conversation: "Also here: Kari", and "Kari is replying …" when the composer of
 * another person has focus and text (design L19). Unobtrusive, in the header, and nothing when alone.
 */
function PresenceLine({ members }: { members: readonly PresenceMember[] }) {
  const { t } = useI18n("communications");
  const replying = members.filter((member) => member.state !== "viewing");
  const here = members.filter((member) => member.state === "viewing");
  if (members.length === 0) return null;
  const names = (list: readonly PresenceMember[]) => list.map((member) => member.name).join(", ");
  return (
    <Group gap="xs" mt={4} aria-live="polite" className="thread-presence">
      <Avatar.Group>
        {members.map((member) => (
          <Tooltip key={member.userId} label={member.name}>
            <Avatar size="xs" radius="xl" name={member.name} color="initials" />
          </Tooltip>
        ))}
      </Avatar.Group>
      {replying.length > 0 && (
        <Badge size="sm" variant="light" color="orange">
          {t("presenceReplying", { count: replying.length, names: names(replying) })}
        </Badge>
      )}
      {here.length > 0 && (
        <Text size="xs" c="dimmed">
          {t("presenceAlsoHere", { names: names(here) })}
        </Text>
      )}
    </Group>
  );
}

/**
 * One message: HTML whenever the message has it, in the sandboxed iframe with its own inline images
 * turned into data: URIs by the page, and Show as text beside it; the text otherwise. An unreadable
 * message says so and offers its original; an inbound one offers its original in a menu.
 */
function MessageCard({
  message,
  conversation,
  onSent,
  connectionButton,
}: {
  message: ConversationMessage;
  conversation: ConversationDetail;
  onSent: () => Promise<unknown>;
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
}) {
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
  const hasConnectionNeeded =
    message.direction === "outbound" && message.deliveries.some((d) => d.error === "connection_needed");

  const retry = useMutation({
    mutationFn: () => retryMessage(conversation.id, message.id),
    onSuccess: onSent,
    onError: (err: Error) => {
      notifications.show({
        color: "red",
        title: t("retryFailed"),
        message: err.message,
      });
    },
  });

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
          {message.sendAs === "personal" && message.fromAddress && (
            <Badge size="sm" variant="light" color="blue">
              {t("fromPersonal", {
                from: message.fromDisplayName
                  ? `${message.fromDisplayName} <${message.fromAddress}>`
                  : message.fromAddress,
                replyTo: message.replyToAddress || conversation.channelAddress || "",
              })}
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
      <AttachmentLinks attachments={message.attachments} />
      {hasConnectionNeeded && (
        <Alert color="red" variant="light" mt="xs" title={t("sendFailedConnectionNeeded")}>
          <Group justify="space-between" align="center" mt="xs">
            {connectionButton?.({ feature: "mail", returnTo: returnHere() })}
            <Button size="xs" color="red" loading={retry.isPending} onClick={() => retry.mutate()}>
              {t("retrySend")}
            </Button>
          </Group>
        </Alert>
      )}
    </article>
  );
}

import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconNote, IconPaperclip, IconPlus, IconSend, IconX } from "@tabler/icons-react";
import { useMutation, useQueries } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AttachmentUpload,
  addConversationNote,
  attachmentUploadStatusQueryOptions,
  type ConversationDetail,
  type MessageComponents,
  replyToConversation,
  stageConversationAttachment,
} from "../api/conversations";
import {
  addOption,
  addYesNo,
  canReplyInWindow,
  characters,
  type DraftOption,
  limitsOf,
  optionsProblem,
  textWithinLimit,
} from "./chat";
import { relative } from "./conversationHelpers";
import { useDraftingSignal } from "./useDraftingSignal";

const errorCode = (error: Error) => ("code" in error && typeof error.code === "string" ? error.code : undefined);

/** The refusals of a chat reply that have a sentence of their own (Messenger design N4, N6, N7). */
const replyErrors: Record<string, string> = {
  reply_window_closed: "chatErrorWindowClosed",
  text_too_long: "chatErrorTextTooLong",
  components_invalid: "chatErrorComponentsInvalid",
  components_unsupported: "chatErrorComponentsUnsupported",
};

export type ChatComposerProps = {
  conversation: ConversationDetail;
  onSent: () => Promise<unknown>;
  /** Called with true while the composer has focus and text, so the thread announces "replying". */
  onDraftingChange?: (drafting: boolean) => void;
};

/**
 * The composer of a Messenger conversation (design N8): plain text with no subject, no Cc and no rich
 * formatting, attachments, the reply window's state, and the answer options — *Add Yes / No*, *Add
 * option* up to the provider's limit, each with a remove × and a counter against its title limit —
 * and *Ask for e-mail*. A closed window disables sending, with the sentence that says why; a note can
 * still be added.
 */
export function ChatComposer({ conversation, onSent, onDraftingChange }: ChatComposerProps) {
  const { t } = useI18n("communications");
  const limits = limitsOf(conversation);
  const window = conversation.replyWindow ?? null;
  const [noteMode, setNoteMode] = useState(false);
  const [text, setText] = useState("");
  const drafting = useDraftingSignal(text.trim() !== "", onDraftingChange);
  const [options, setOptions] = useState<DraftOption[]>([]);
  const [shareEmail, setShareEmail] = useState(false);
  const [uploads, setUploads] = useState<AttachmentUpload[]>([]);
  const statusQueries = useQueries({
    queries: uploads.map((item) => attachmentUploadStatusQueryOptions(conversation.id, item.id)),
  });
  const currentUploads = uploads.map((item, index) => statusQueries[index]?.data ?? item);
  const waitingForScan = currentUploads.some((file) => !file.ready);

  const send = useMutation({
    mutationFn: () => {
      if (noteMode) return addConversationNote(conversation.id, text);
      const components: MessageComponents = {
        ...(options.length > 0
          ? { answerOptions: options.map((option) => ({ id: option.id, title: option.title.trim() })) }
          : {}),
        ...(shareEmail ? { shareEmail: true } : {}),
      };
      return replyToConversation(
        conversation.id,
        {
          textBody: text,
          replyMode: "reply",
          attachmentIds: currentUploads.map((item) => item.id),
          ...(Object.keys(components).length > 0 ? { components } : {}),
        },
        crypto.randomUUID(),
      );
    },
    onSuccess: async () => {
      setText("");
      setOptions([]);
      setShareEmail(false);
      setUploads([]);
      await onSent();
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => stageConversationAttachment(conversation.id, file, crypto.randomUUID()),
    onSuccess: (item) => setUploads((items) => [...items, item]),
  });

  const windowClosed = !canReplyInWindow(window);
  // The request for an e-mail address is a quick reply too: it takes one of the slots.
  const reserved = shareEmail ? 1 : 0;
  const problem = optionsProblem(options, limits, reserved);
  const blocked = noteMode
    ? !text.trim()
    : windowClosed ||
      (!text.trim() && currentUploads.length === 0) ||
      !textWithinLimit(text, limits) ||
      problem !== null ||
      waitingForScan ||
      !conversation.replyRecipients.canReply;
  const closesAt = window?.closesAt ? relative(window.closesAt) : "";

  return (
    <footer className="composer chat-composer" {...drafting}>
      <Group justify="space-between" mb="xs">
        <Text fw={700}>{noteMode ? t("internalNote") : t("reply")}</Text>
        <Button
          size="xs"
          variant="subtle"
          onClick={() => setNoteMode((value) => !value)}
          leftSection={<IconNote size={14} />}
        >
          {noteMode ? t("reply") : t("addNote")}
        </Button>
      </Group>
      {!noteMode && window?.state === "closed" && (
        <Alert color="orange" variant="light" mb="xs" title={t("chatWindowClosedTitle")}>
          {t("chatWindowClosed")}
        </Alert>
      )}
      {!noteMode && window?.state === "human_agent" && (
        <Alert color="yellow" variant="light" mb="xs" title={t("chatWindowHumanAgentTitle")}>
          {t("chatWindowHumanAgent", { time: closesAt })}
        </Alert>
      )}
      {!noteMode && window?.state === "open" && closesAt && (
        <Text size="xs" c="dimmed" mb="xs">
          {t("chatWindowOpen", { time: closesAt })}
        </Text>
      )}
      <Textarea
        aria-label={noteMode ? t("internalNote") : t("message")}
        rows={3}
        styles={{ input: { resize: "vertical" } }}
        value={text}
        onChange={(event) => setText(event.currentTarget.value)}
        error={!noteMode && !textWithinLimit(text, limits) ? t("chatTextTooLong", { max: limits.maxText }) : undefined}
      />
      {!noteMode && (
        <Text size="xs" c={textWithinLimit(text, limits) ? "dimmed" : "red"} ta="right" mt={2}>
          {characters(text)} / {limits.maxText}
        </Text>
      )}
      {!noteMode && (
        <Card withBorder radius="md" padding="sm" mt="xs" className="answer-options">
          <Group justify="space-between" mb={options.length > 0 ? "xs" : 0}>
            <Text size="sm" fw={600}>
              {t("answerOptions")}
            </Text>
            <Group gap="xs">
              <Button
                size="compact-xs"
                variant="light"
                disabled={options.length + reserved + 2 > limits.maxAnswerOptions}
                onClick={() => setOptions((items) => addYesNo(items, { yes: t("yes"), no: t("no") }, limits, reserved))}
              >
                {t("addYesNo")}
              </Button>
              <Button
                size="compact-xs"
                variant="light"
                leftSection={<IconPlus size={12} />}
                disabled={options.length + reserved >= limits.maxAnswerOptions}
                onClick={() => setOptions((items) => addOption(items, limits, reserved))}
              >
                {t("addOption")}
              </Button>
              {limits.shareEmail && (
                <Checkbox
                  size="xs"
                  label={t("askForEmail")}
                  checked={shareEmail}
                  disabled={!shareEmail && options.length >= limits.maxAnswerOptions}
                  onChange={(event) => setShareEmail(event.currentTarget.checked)}
                />
              )}
            </Group>
          </Group>
          {options.length > 0 && (
            <Stack gap={6}>
              {options.map((option, index) => {
                const used = characters(option.title.trim());
                const over = used > limits.maxAnswerTitle;
                return (
                  <TextInput
                    key={option.id}
                    size="xs"
                    aria-label={t("answerOptionN", { n: index + 1 })}
                    value={option.title}
                    error={over ? t("answerOptionTooLong", { max: limits.maxAnswerTitle }) : undefined}
                    onChange={(event) => {
                      const title = event.currentTarget.value;
                      setOptions((items) => items.map((item) => (item.id === option.id ? { ...item, title } : item)));
                    }}
                    rightSectionWidth={72}
                    rightSection={
                      <Group gap={4} wrap="nowrap">
                        <Text size="xs" c={over ? "red" : "dimmed"} data-testid="option-counter">
                          {used}/{limits.maxAnswerTitle}
                        </Text>
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="gray"
                          aria-label={t("removeOption", { n: index + 1 })}
                          onClick={() => setOptions((items) => items.filter((item) => item.id !== option.id))}
                        >
                          <IconX size={12} />
                        </ActionIcon>
                      </Group>
                    }
                  />
                );
              })}
            </Stack>
          )}
        </Card>
      )}
      <Group justify="space-between" mt="xs">
        <Button component="label" size="xs" variant="subtle" leftSection={<IconPaperclip size={14} />}>
          {t("attachFile")}
          <input
            hidden
            type="file"
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              if (file) upload.mutate(file);
              event.currentTarget.value = "";
            }}
          />
        </Button>
        {uploads.length > 0 && (
          <Group gap="xs">
            {currentUploads.map((file) => (
              <Badge key={file.id} color={file.ready ? "teal" : file.scanStatus === "pending" ? "yellow" : "red"}>
                {file.fileName}
                {file.ready ? "" : file.scanStatus === "pending" ? ` · ${t("scanning")}` : ` · ${t("unavailable")}`}
              </Badge>
            ))}
          </Group>
        )}
      </Group>
      {upload.error && (
        <Text size="xs" c="red">
          {errorCode(upload.error) === "attachment_too_large"
            ? t("attachmentTooLarge")
            : t("attachmentCouldNotBeStaged")}
        </Text>
      )}
      {send.error && (
        <Text size="xs" c="red" mt="xs">
          {t(replyErrors[errorCode(send.error) ?? ""] ?? (noteMode ? "noteNotAdded" : "replyNotSent"), {
            max: limits.maxText,
          })}
        </Text>
      )}
      <Group justify="end" mt="sm">
        <Button
          leftSection={<IconSend size={15} />}
          loading={send.isPending}
          disabled={blocked}
          onClick={() => send.mutate()}
        >
          {waitingForScan ? t("waitingForAttachmentScan") : noteMode ? t("addNote") : t("sendReply")}
        </Button>
      </Group>
    </footer>
  );
}

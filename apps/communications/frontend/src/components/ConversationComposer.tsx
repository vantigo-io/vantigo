import { Badge, Button, Card, Checkbox, Group, Select, Text, TextInput } from "@mantine/core";
import { RichTextEditor } from "@mantine/tiptap";
import { IconNote, IconPaperclip, IconSend, IconSparkles } from "@tabler/icons-react";
import { useMutation, useQueries } from "@tanstack/react-query";
import { useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useState } from "react";
import {
  type AttachmentUpload,
  addConversationNote,
  attachmentUploadStatusQueryOptions,
  type ConversationDetail,
  draftConversationWithAi,
  replyToConversation,
  stageConversationAttachment,
} from "../api/conversations";
import { returnHere } from "./conversationHelpers";

export type ConnectionButtonProps = {
  feature: "calendar" | "mail";
  returnTo: string;
};

const errorCode = (error: Error) => ("code" in error && typeof error.code === "string" ? error.code : undefined);

export type ConversationComposerProps = {
  conversation: ConversationDetail;
  onSent: () => Promise<unknown>;
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
};

export function ConversationComposer({ conversation, onSent, connectionButton }: ConversationComposerProps) {
  const { t, formatters } = useI18n("communications");
  // The attachments the channel takes, as the server words them in its refusal: never the budget of MIME.
  const allowance = conversation.maxAttachmentBytes ?? null;
  const allowanceMb =
    allowance === null ? "" : formatters.formatNumber(allowance / 1_000_000, { maximumFractionDigits: 1 });
  const [noteMode, setNoteMode] = useState(false);
  // A reply answers the latest inbound message; with none yet there is no one to send it to.
  const canReply = conversation.replyRecipients.canReply;
  const [body, setBody] = useState("");
  const [replyMode, setReplyMode] = useState<"reply" | "reply_all">("reply");
  const [sendFromPersonal, setSendFromPersonal] = useState(false);
  const [uploads, setUploads] = useState<AttachmentUpload[]>([]);
  const [aiTone, setAiTone] = useState<"concise" | "friendly" | "formal">("friendly");
  const [aiInstruction, setAiInstruction] = useState("");
  const [aiDraft, setAiDraft] = useState(false);
  const editor = useEditor({ extensions: [StarterKit], content: "" });
  const statusQueries = useQueries({
    queries: uploads.map((item) => attachmentUploadStatusQueryOptions(conversation.id, item.id)),
  });
  const currentUploads = uploads.map((item, index) => statusQueries[index]?.data ?? item);
  const send = useMutation({
    mutationFn: () =>
      noteMode
        ? addConversationNote(conversation.id, editor?.getText() || body)
        : replyToConversation(
            conversation.id,
            {
              textBody: editor?.getText() || body,
              htmlBody: editor?.getHTML() || undefined,
              replyMode,
              attachmentIds: currentUploads.map((item) => item.id),
              sendFromPersonal: conversation.personalSender?.available ? sendFromPersonal : undefined,
            },
            crypto.randomUUID(),
          ),
    onSuccess: async () => {
      setBody("");
      editor?.commands.clearContent();
      setUploads([]);
      await onSent();
    },
  });
  const upload = useMutation({
    mutationFn: (file: File) => stageConversationAttachment(conversation.id, file, crypto.randomUUID()),
    onSuccess: (item) => setUploads((items) => [...items, item]),
  });
  const draft = useMutation({
    mutationFn: () => draftConversationWithAi(conversation.id, { tone: aiTone, instruction: aiInstruction.trim() }),
    onSuccess: (result) => {
      editor?.commands.setContent(result.text);
      setAiDraft(true);
    },
  });

  return (
    <footer className="composer">
      {!noteMode && (
        <Card withBorder radius="md" padding="sm" mb="sm" className="ai-draft-panel">
          <Group justify="space-between">
            <Text size="sm" fw={700}>
              <IconSparkles size={15} /> {t("draftWithAi")}
            </Text>
            <Select
              size="xs"
              value={aiTone}
              onChange={(value) => value && setAiTone(value as typeof aiTone)}
              data={["concise", "friendly", "formal"]}
              aria-label={t("aiTone")}
            />
          </Group>
          <TextInput
            size="sm"
            mt="xs"
            value={aiInstruction}
            onChange={(event) => setAiInstruction(event.currentTarget.value)}
            placeholder={t("aiReplyInstruction")}
          />
          <Button
            size="xs"
            mt="xs"
            variant="light"
            loading={draft.isPending}
            disabled={!aiInstruction.trim()}
            onClick={() => draft.mutate()}
          >
            {" "}
            {aiDraft ? t("regenerateDraft") : t("draftWithAi")}
          </Button>
          {aiDraft && (
            <Text size="xs" c="dimmed" mt="xs">
              {t("aiDraftReview")}
            </Text>
          )}
          {draft.error && (
            <Text size="xs" c="red" mt="xs">
              {draft.error.message.includes("503") ? t("aiDraftUnavailable") : draft.error.message}
            </Text>
          )}
        </Card>
      )}
      <Group justify="space-between" mb="xs">
        <Group gap="xs">
          <Text fw={700}>{noteMode ? t("internalNote") : aiDraft ? t("aiDraftReview") : t("reply")}</Text>
          {!noteMode && conversation.replyRecipients.canReplyAll && (
            <Select
              size="xs"
              value={replyMode}
              onChange={(value) => value && setReplyMode(value as typeof replyMode)}
              data={[
                { value: "reply", label: t("reply") },
                {
                  value: "reply_all",
                  label: t("replyAll", { count: conversation.replyRecipients.replyAllCc.length + 1 }),
                },
              ]}
              aria-label={t("replyMode")}
            />
          )}
        </Group>
        <Button
          size="xs"
          variant="subtle"
          onClick={() => setNoteMode((value) => !value)}
          leftSection={<IconNote size={14} />}
        >
          {noteMode ? t("reply") : t("addNote")}
        </Button>
      </Group>
      {!noteMode && conversation.replyRecipients.replyTo && (
        <Text size="xs" c="dimmed" mb="xs">
          {t("replyingTo", { address: conversation.replyRecipients.replyTo })}
          {replyMode === "reply_all"
            ? t("additionalRecipient", { count: conversation.replyRecipients.replyAllCc.length })
            : ""}
        </Text>
      )}
      {!noteMode && conversation.personalSender?.available && (
        <Checkbox
          size="xs"
          mb="xs"
          label={t("sendFromPersonal", { address: conversation.personalSender.address ?? "" })}
          checked={sendFromPersonal}
          onChange={(event) => setSendFromPersonal(event.currentTarget.checked)}
        />
      )}
      {!noteMode &&
        !conversation.personalSender?.available &&
        (conversation.personalSender?.reason === "not_connected" ||
          conversation.personalSender?.reason === "reconnect") &&
        connectionButton && (
          <Group mb="xs" gap="xs">
            {connectionButton({ feature: "mail", returnTo: returnHere() })}
          </Group>
        )}
      {!noteMode && !canReply && (
        <Text size="xs" c="dimmed" mb="xs">
          {t("noMessageToReplyTo")}
        </Text>
      )}
      <RichTextEditor editor={editor}>
        <RichTextEditor.Toolbar sticky stickyOffset={0}>
          <RichTextEditor.ControlsGroup>
            <RichTextEditor.Bold />
            <RichTextEditor.Italic />
            <RichTextEditor.BulletList />
          </RichTextEditor.ControlsGroup>
        </RichTextEditor.Toolbar>
        <RichTextEditor.Content />
      </RichTextEditor>
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
        {allowance !== null && (
          <Text size="xs" c="dimmed">
            {t("attachmentsLimit", { size: allowanceMb })}
          </Text>
        )}
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
            ? allowance !== null
              ? t("attachmentTooLargeForChannel", { size: allowanceMb })
              : t("attachmentTooLarge")
            : t("attachmentCouldNotBeStaged")}
        </Text>
      )}
      {send.error && (
        <Text size="xs" c="red" mt="xs">
          {errorCode(send.error) === "too_large"
            ? t("messageTooLargeForChannel")
            : noteMode
              ? t("noteNotAdded")
              : t("replyNotSent")}
        </Text>
      )}
      <Group justify="end" mt="sm">
        <Button
          leftSection={<IconSend size={15} />}
          loading={send.isPending}
          disabled={
            (!noteMode && !canReply) ||
            (!editor?.getText() && !body.trim()) ||
            currentUploads.some((file) => !file.ready)
          }
          onClick={() => send.mutate()}
        >
          {currentUploads.some((file) => !file.ready)
            ? t("waitingForAttachmentScan")
            : noteMode
              ? t("addNote")
              : t("sendReply")}
        </Button>
      </Group>
    </footer>
  );
}

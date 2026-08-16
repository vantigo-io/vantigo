import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Divider,
  Group,
  Loader,
  Menu,
  ScrollArea,
  Select,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { RichTextEditor } from "@mantine/tiptap";
import {
  IconArchive,
  IconCheck,
  IconChevronDown,
  IconInbox,
  IconNote,
  IconPaperclip,
  IconSend,
  IconSparkles,
  IconTag,
} from "@tabler/icons-react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AttachmentUpload,
  addConversationNote,
  addTag,
  attachmentDownloadUrl,
  attachmentUploadStatusQueryOptions,
  type ConversationDetail,
  type ConversationStatus,
  conversationQueryOptions,
  conversationsQueryOptions,
  draftConversationWithAi,
  markConversationRead,
  removeTag,
  replyToConversation,
  safeHtmlSrcDoc,
  stageConversationAttachment,
  suggestConversationCustomerWithAi,
  tagsQueryOptions,
  updateConversation,
} from "../api/conversations";
import "../i18n";
import "../styles.css";

const relative = (date: string) =>
  new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" }).format(new Date(date));
const participantName = (item: { displayName: string | null; address: string }) => item.displayName || item.address;

export function InboxPage() {
  const { t } = useI18n("communications");
  const search = useSearch({ strict: false }) as {
    conversationId?: string;
    status?: ConversationStatus;
    customerId?: number;
    unreadOnly?: boolean;
    tagId?: string;
  };
  const navigate = useNavigate() as (options: unknown) => void;
  const filters = {
    page: 1,
    pageSize: 30,
    status: search.status,
    customerId: search.customerId,
    unreadOnly: search.unreadOnly,
    tagId: search.tagId,
  };
  const list = useQuery(conversationsQueryOptions(filters));
  const selectedId = search.conversationId || list.data?.data[0]?.id || "";
  const detail = useQuery(conversationQueryOptions(selectedId));
  const tags = useQuery(tagsQueryOptions());
  const client = useQueryClient();
  const [mobileFilters, setMobileFilters] = useState(false);
  const invalidate = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: ["conversations"] }),
      client.invalidateQueries({ queryKey: ["conversation", selectedId] }),
    ]);
  const select = (id: string) => void navigate({ search: { ...search, conversationId: id } });
  const active = detail.data;
  const markRead = useMutation({ mutationFn: () => markConversationRead(selectedId), onSuccess: invalidate });
  const workflow = useMutation({
    mutationFn: (body: { status?: ConversationStatus; assignedUserId?: string | null; customerId?: number | null }) =>
      updateConversation(selectedId, body),
    onSuccess: invalidate,
  });
  const tagMutation = useMutation({
    mutationFn: ({ tagId, remove }: { tagId: string; remove?: boolean }) =>
      remove ? removeTag(selectedId, tagId) : addTag(selectedId, tagId),
    onSuccess: invalidate,
  });
  const suggest = useMutation({
    mutationFn: () => suggestConversationCustomerWithAi(selectedId),
    onSuccess: invalidate,
  });
  return (
    <Stack gap="lg" className="inbox-shell">
      <PageHeader eyebrow={t("communications")} title={t("inbox")} description={t("inboxDescription")} />
      <Card withBorder radius="lg" padding={0} className="inbox-card">
        <Group className="inbox-toolbar" justify="space-between" p="sm">
          <Group gap="xs">
            <IconInbox size={19} />
            <Text fw={700}>{t("sharedInbox")}</Text>
          </Group>
          <Button variant="subtle" hiddenFrom="sm" onClick={() => setMobileFilters((v) => !v)}>
            {t("filters")}
          </Button>
          <TextInput placeholder={t("searchConversations")} size="sm" className="inbox-search" disabled />
        </Group>
        <div className={`inbox-grid ${mobileFilters ? "show-filters" : ""}`}>
          <aside className="inbox-filters">
            <Text size="xs" tt="uppercase" fw={700} c="dimmed">
              {t("views")}
            </Text>
            {([undefined, "open", "closed", "archived"] as const).map((status) => (
              <Button
                key={status || "all"}
                variant={search.status === status ? "light" : "subtle"}
                justify="start"
                fullWidth
                onClick={() => void navigate({ search: { ...search, status, conversationId: undefined } })}
              >
                {status ? status[0].toUpperCase() + status.slice(1) : t("allConversations")}
              </Button>
            ))}
            <Divider my="sm" />
            <Button
              variant={search.unreadOnly ? "light" : "subtle"}
              justify="start"
              fullWidth
              onClick={() =>
                void navigate({
                  search: { ...search, unreadOnly: search.unreadOnly ? undefined : true, conversationId: undefined },
                })
              }
            >
              {t("unread")}
            </Button>
            <Text size="xs" tt="uppercase" fw={700} c="dimmed" mt="md">
              {t("tags")}
            </Text>
            {tags.data?.map((tag) => (
              <Button
                key={tag.id}
                variant={search.tagId === tag.id ? "light" : "subtle"}
                justify="start"
                fullWidth
                leftSection={<IconTag size={14} />}
                onClick={() =>
                  void navigate({
                    search: {
                      ...search,
                      tagId: search.tagId === tag.id ? undefined : tag.id,
                      conversationId: undefined,
                    },
                  })
                }
              >
                {tag.name}
              </Button>
            ))}
          </aside>
          <section className="conversation-list">
            <Group justify="space-between" px="md" py="sm">
              <Text fw={700}>{t("conversationsCount", { count: list.data?.pagination.totalCount ?? 0 })}</Text>
              <ActionIcon variant="subtle" aria-label={t("refresh")} onClick={() => void list.refetch()}>
                <IconCheck size={16} />
              </ActionIcon>
            </Group>
            {list.isPending ? (
              <Loader m="md" />
            ) : list.isError ? (
              <Alert m="md" color="red">
                {t("couldNotLoadConversations", { error: list.error.message })}
              </Alert>
            ) : list.data?.data.length === 0 ? (
              <Text c="dimmed" p="md">
                {t("noConversationsMatch")}
              </Text>
            ) : (
              list.data?.data.map((item) => (
                <button
                  type="button"
                  className={`conversation-row ${item.id === selectedId ? "selected" : ""} ${item.unread ? "unread" : ""}`}
                  key={item.id}
                  onClick={() => select(item.id)}
                >
                  <Group justify="space-between" wrap="nowrap">
                    <Text fw={item.unread ? 800 : 600} lineClamp={1}>
                      {item.participants[0] ? participantName(item.participants[0]) : "Unknown participant"}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {relative(item.lastActivityAt)}
                    </Text>
                  </Group>
                  <Text size="sm" fw={item.unread ? 700 : 500} lineClamp={1}>
                    {item.subject || "No subject"}
                  </Text>
                  <Text size="xs" c="dimmed" lineClamp={1}>
                    {item.previewText || "No preview"}
                  </Text>
                  <Group gap={4} mt={5}>
                    {item.tags.map((tag) => (
                      <Badge key={tag.id} size="xs" variant="light" color={tag.color || "indigo"}>
                        {tag.name}
                      </Badge>
                    ))}
                    <Badge size="xs" variant="light" color={item.status === "open" ? "teal" : "gray"}>
                      {item.status}
                    </Badge>
                  </Group>
                </button>
              ))
            )}
          </section>
          <main className="thread-pane">
            {!selectedId ? (
              <Stack align="center" justify="center" h="100%" p="xl">
                <IconInbox size={38} color="var(--mantine-color-dimmed)" />
                <Title order={3}>{t("selectConversation")}</Title>
                <Text c="dimmed">{t("repliesAndNotesTogether")}</Text>
              </Stack>
            ) : detail.isPending ? (
              <Loader m="xl" />
            ) : detail.isError ? (
              <Alert m="md" color="red">
                {t("couldNotLoadConversation")}
              </Alert>
            ) : (
              active && (
                <Thread
                  key={active.id}
                  conversation={active}
                  tags={tags.data || []}
                  onRead={() => markRead.mutate()}
                  onWorkflow={(body) => workflow.mutate(body)}
                  onTag={(tagId, remove) => tagMutation.mutate({ tagId, remove })}
                  onSent={invalidate}
                  onSuggest={() => suggest.mutate()}
                  suggestionPending={suggest.isPending}
                  suggestionError={suggest.error}
                />
              )
            )}
          </main>
        </div>
      </Card>
    </Stack>
  );
}

function Thread({
  conversation,
  tags,
  onRead,
  onWorkflow,
  onTag,
  onSent,
  onSuggest,
  suggestionPending,
  suggestionError,
}: {
  conversation: ConversationDetail;
  tags: { id: string; name: string; color: string | null }[];
  onRead: () => void;
  onWorkflow: (body: { status?: ConversationStatus; customerId?: number | null }) => void;
  onTag: (id: string, remove?: boolean) => void;
  onSent: () => Promise<unknown>;
  onSuggest: () => void;
  suggestionPending: boolean;
  suggestionError: Error | null;
}) {
  const { t } = useI18n("communications");
  const [noteMode, setNoteMode] = useState(false);
  const [body, setBody] = useState("");
  const [replyMode, setReplyMode] = useState<"reply" | "reply_all">("reply");
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
          <article key={message.id} className={`message-card ${message.direction}`}>
            <Group justify="space-between">
              <Text fw={700}>
                {message.direction === "internal_note"
                  ? "Internal note"
                  : message.direction === "outbound"
                    ? "You"
                    : message.participant
                      ? participantName(message.participant)
                      : "Customer"}
              </Text>
              <Text size="xs" c="dimmed">
                {relative(message.occurredAt)}
              </Text>
            </Group>
            {message.textBody && (
              <Text mt="xs" style={{ whiteSpace: "pre-wrap" }}>
                {message.textBody}
              </Text>
            )}
            {!message.textBody && message.htmlBody && (
              <iframe
                title={t("messageHtml")}
                sandbox=""
                srcDoc={safeHtmlSrcDoc(message.htmlBody)}
                className="message-html"
              />
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
                  ) : (
                    <Badge key={file.id} leftSection={<IconPaperclip size={12} />} variant="outline">
                      {file.scanStatus === "pending" ? t("scanning") : t("attachmentUnavailable")}
                    </Badge>
                  ),
                )}
              </Group>
            )}
          </article>
        ))}
      </ScrollArea>
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
            onClick={() => setNoteMode((v) => !v)}
            leftSection={<IconNote size={14} />}
          >
            {noteMode ? t("reply") : t("addNote")}
          </Button>
        </Group>
        {!noteMode && conversation.replyRecipients.replyTo && (
          <Text size="xs" c="dimmed" mb="xs">
            {t("replyingTo", { address: conversation.replyRecipients.replyTo })}
            {replyMode === "reply_all"
              ? t("additionalRecipient", {
                  count: conversation.replyRecipients.replyAllCc.length,
                  suffix: conversation.replyRecipients.replyAllCc.length === 1 ? "" : "s",
                })
              : ""}
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
            {t("attachmentCouldNotBeStaged")}
          </Text>
        )}
        <Group justify="end" mt="sm">
          <Button
            leftSection={<IconSend size={15} />}
            loading={send.isPending}
            disabled={(!editor?.getText() && !body.trim()) || currentUploads.some((file) => !file.ready)}
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
    </div>
  );
}

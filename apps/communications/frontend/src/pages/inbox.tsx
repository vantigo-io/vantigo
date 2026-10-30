import { Alert, Button, Card, Checkbox, Group, Modal, Stack, Text, Textarea, TextInput, Title } from "@mantine/core";
import { IconInbox, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { useState } from "react";
import {
  addTag,
  type ConversationStatus,
  conversationQueryOptions,
  conversationsQueryOptions,
  createConversation,
  markConversationRead,
  personalSenderQueryOptions,
  removeTag,
  suggestConversationCustomerWithAi,
  tagsQueryOptions,
  updateConversation,
} from "../api/conversations";
import type { ConnectionButtonProps } from "../components/ConversationComposer";
import { ConversationList } from "../components/ConversationList";
import { ConversationThread } from "../components/ConversationThread";
import { InboxFilters, type InboxSearch } from "../components/InboxFilters";
import "../i18n";
import "../styles.css";

export type InboxPageProps = {
  connectionButton?: (props: ConnectionButtonProps) => ReactNode;
};

export function InboxPage({ connectionButton }: InboxPageProps = {}) {
  const { t } = useI18n("communications");
  const search = useSearch({ strict: false }) as InboxSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const [newModalOpen, setNewModalOpen] = useState(false);
  const [newTo, setNewTo] = useState("");
  const [newSubject, setNewSubject] = useState("");
  const [newBody, setNewBody] = useState("");
  const [sendFromPersonal, setSendFromPersonal] = useState(false);
  const personalSenderQuery = useQuery(personalSenderQueryOptions());
  const personalSender = personalSenderQuery.data;
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
  const create = useMutation({
    mutationFn: () =>
      createConversation(
        {
          to: [{ email: newTo.trim() }],
          subject: newSubject.trim(),
          textBody: newBody.trim(),
          sendFromPersonal: personalSender?.available ? sendFromPersonal : undefined,
        },
        crypto.randomUUID(),
      ),
    onSuccess: async (answer) => {
      setNewModalOpen(false);
      setNewTo("");
      setNewSubject("");
      setNewBody("");
      setSendFromPersonal(false);
      await client.invalidateQueries({ queryKey: ["conversations"] });
      if (answer.conversationId) {
        select(answer.conversationId);
      }
    },
  });

  return (
    <Stack gap="lg" className="inbox-shell">
      <PageHeader title={t("inbox")} description={t("inboxDescription")} />
      <Card withBorder radius="lg" padding={0} className="inbox-card">
        <Group className="inbox-toolbar" justify="space-between" p="sm">
          <Group gap="xs">
            <IconInbox size={19} />
            <Text fw={700}>{t("sharedInbox")}</Text>
          </Group>
          <InboxFilters
            search={search}
            tags={tags.data || []}
            onSearchChange={(nextSearch) => void navigate({ search: nextSearch })}
          />
          <Group gap="xs">
            <TextInput placeholder={t("searchConversations")} size="sm" className="inbox-search" disabled />
            <Button size="sm" leftSection={<IconPlus size={15} />} onClick={() => setNewModalOpen(true)}>
              {t("newConversation")}
            </Button>
          </Group>
        </Group>
        <div className="inbox-grid">
          <ConversationList
            data={list.data}
            isPending={list.isPending}
            isError={list.isError}
            error={list.error}
            selectedId={selectedId}
            onSelect={select}
            onRefresh={() => void list.refetch()}
          />
          <main className="thread-pane">
            {!selectedId ? (
              <Stack align="center" justify="center" h="100%" p="xl">
                <IconInbox size={38} color="var(--mantine-color-dimmed)" />
                <Title order={3}>{t("selectConversation")}</Title>
                <Text c="dimmed">{t("repliesAndNotesTogether")}</Text>
              </Stack>
            ) : detail.isPending ? (
              <ContentSkeleton rows={5} p="md" />
            ) : detail.isError ? (
              <Alert m="md" color="red">
                {t("couldNotLoadConversation")}
              </Alert>
            ) : (
              active && (
                <ConversationThread
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
                  connectionButton={connectionButton}
                />
              )
            )}
          </main>
        </div>
      </Card>
      <Modal opened={newModalOpen} onClose={() => setNewModalOpen(false)} title={t("newConversation")} size="lg">
        <Stack gap="sm">
          <TextInput label={t("to")} required value={newTo} onChange={(e) => setNewTo(e.currentTarget.value)} />
          <TextInput
            label={t("subject")}
            required
            value={newSubject}
            onChange={(e) => setNewSubject(e.currentTarget.value)}
          />
          <Textarea
            label={t("message")}
            required
            minRows={4}
            value={newBody}
            onChange={(e) => setNewBody(e.currentTarget.value)}
          />
          {personalSender?.available && (
            <Checkbox
              size="xs"
              label={t("sendFromPersonal", { address: personalSender.address ?? "" })}
              checked={sendFromPersonal}
              onChange={(e) => setSendFromPersonal(e.currentTarget.checked)}
            />
          )}
          {!personalSender?.available &&
            (personalSender?.reason === "not_connected" || personalSender?.reason === "reconnect") &&
            connectionButton && (
              <Group gap="xs">{connectionButton({ feature: "mail", returnTo: "/communications/inbox" })}</Group>
            )}
          {create.error && (
            <Text size="xs" c="red">
              {create.error.message}
            </Text>
          )}
          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setNewModalOpen(false)}>
              {t("cancel")}
            </Button>
            <Button
              loading={create.isPending}
              disabled={!newTo.trim() || !newSubject.trim() || !newBody.trim()}
              onClick={() => create.mutate()}
            >
              {t("send")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}

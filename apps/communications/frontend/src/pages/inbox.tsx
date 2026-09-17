import { Alert, Button, Card, Group, Stack, Text, TextInput, Title } from "@mantine/core";
import { IconInbox } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  addTag,
  type ConversationStatus,
  conversationQueryOptions,
  conversationsQueryOptions,
  markConversationRead,
  removeTag,
  suggestConversationCustomerWithAi,
  tagsQueryOptions,
  updateConversation,
} from "../api/conversations";
import { ConversationList } from "../components/ConversationList";
import { ConversationThread } from "../components/ConversationThread";
import { InboxFilters, type InboxSearch } from "../components/InboxFilters";
import "../i18n";
import "../styles.css";

export function InboxPage() {
  const { t } = useI18n("communications");
  const search = useSearch({ strict: false }) as InboxSearch;
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
      <PageHeader title={t("inbox")} description={t("inboxDescription")} />
      <Card withBorder radius="lg" padding={0} className="inbox-card">
        <Group className="inbox-toolbar" justify="space-between" p="sm">
          <Group gap="xs">
            <IconInbox size={19} />
            <Text fw={700}>{t("sharedInbox")}</Text>
          </Group>
          <Button variant="subtle" hiddenFrom="sm" onClick={() => setMobileFilters((value) => !value)}>
            {t("filters")}
          </Button>
          <TextInput placeholder={t("searchConversations")} size="sm" className="inbox-search" disabled />
        </Group>
        <div className={`inbox-grid ${mobileFilters ? "show-filters" : ""}`}>
          <InboxFilters
            search={search}
            tags={tags.data || []}
            onSearchChange={(nextSearch) => void navigate({ search: nextSearch })}
          />
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
                />
              )
            )}
          </main>
        </div>
      </Card>
    </Stack>
  );
}

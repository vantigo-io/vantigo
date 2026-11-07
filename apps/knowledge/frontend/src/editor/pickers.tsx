import { Avatar, Group, Stack, Text } from "@mantine/core";
import { IconFileText } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import type { Editor } from "@tiptap/core";
import { useI18n } from "@vantigo/frontend-shell";
import { useSyncExternalStore } from "react";
import { assignableUsersQueryOptions, searchQueryOptions } from "../api/articles";
import { SuggestionMenu } from "./suggestion-menu";

const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part.charAt(0).toLocaleUpperCase())
    .join("");

/** The `@` picker (knowledge design K20): the directory's people matching what was typed. */
export const MentionPicker = ({ editor }: { editor: Editor }) => {
  const { t } = useI18n("knowledge");
  const store = editor.storage.mention.store;
  const { active, query } = useSyncExternalStore(store.subscribe, store.get);
  const users = useQuery({ ...assignableUsersQueryOptions(query), enabled: active });
  return (
    <SuggestionMenu
      store={store}
      items={active ? (users.data ?? []) : []}
      itemKey={(user) => user.userId}
      label={t("mentionPickerLabel")}
      empty={users.isFetching ? t("pickerLoading") : t("mentionNothing")}
      loading={users.isFetching}
      onChoose={(user, range, current) =>
        current
          .chain()
          .focus()
          .insertContentAt(range, [
            { type: "mention", attrs: { id: user.userId, label: user.displayName } },
            { type: "text", text: " " },
          ])
          .run()
      }
      renderItem={(user) => (
        <Group gap="sm" wrap="nowrap">
          <Avatar size="sm" radius="xl">
            {initials(user.displayName)}
          </Avatar>
          <Text size="sm">{user.displayName}</Text>
        </Group>
      )}
    />
  );
};

/** The `[[` picker (K21): the pages the person may read that match what was typed. */
export const ArticleLinkPicker = ({ editor, articleId }: { editor: Editor; articleId?: string }) => {
  const { t } = useI18n("knowledge");
  const store = editor.storage.articleLinkPicker.store;
  const { active, query } = useSyncExternalStore(store.subscribe, store.get);
  const hits = useQuery({ ...searchQueryOptions(query, 10), enabled: active && query.trim().length > 0 });
  const items = active && query.trim() ? (hits.data ?? []).filter((hit) => hit.id !== articleId) : [];
  return (
    <SuggestionMenu
      store={store}
      items={items}
      itemKey={(hit) => hit.id}
      label={t("articlePickerLabel")}
      empty={!query.trim() ? t("articlePickerHint") : hits.isFetching ? t("pickerLoading") : t("searchNothing")}
      loading={hits.isFetching}
      onChoose={(hit, range, current) =>
        current
          .chain()
          .focus()
          .insertContentAt(range, [
            { type: "articleLink", attrs: { id: hit.id, label: null } },
            { type: "text", text: " " },
          ])
          .run()
      }
      renderItem={(hit) => (
        <Group gap="sm" wrap="nowrap">
          {hit.icon ? <span aria-hidden>{hit.icon}</span> : <IconFileText size={16} aria-hidden />}
          <Stack gap={0} style={{ minWidth: 0 }}>
            <Text size="sm" truncate>
              {hit.title || t("untitled")}
            </Text>
            {hit.path.length > 0 && (
              <Text size="xs" c="dimmed" truncate>
                {hit.path.map((item) => item.title || t("untitled")).join(" / ")}
              </Text>
            )}
          </Stack>
        </Group>
      )}
    />
  );
};

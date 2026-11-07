import { Loader, Stack, Text, TextInput, UnstyledButton } from "@mantine/core";
import { IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { searchQueryOptions } from "../api/articles";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";
import { ArticleIcon } from "./article-icon";
import { Excerpt } from "./excerpt";

/** How long typing must pause before the search is sent. */
export const SEARCH_DEBOUNCE_MS = 250;

/** The search box in the sidebar's header (knowledge design K12). */
export const SidebarSearchBox = ({ value, onChange }: { value: string; onChange: (value: string) => void }) => {
  const { t } = useI18n("knowledge");
  return (
    <TextInput
      size="xs"
      value={value}
      onChange={(event) => onChange(event.currentTarget.value)}
      placeholder={t("searchPlaceholder")}
      aria-label={t("searchLabel")}
      leftSection={<IconSearch size={14} />}
    />
  );
};

/**
 * What the sidebar shows instead of its tree while there is a search: the
 * hits, best first. `query` is already debounced by the caller.
 */
export const SearchResults = ({ query, onOpen }: { query: string; onOpen?: () => void }) => {
  const { t } = useI18n("knowledge");
  const navigate = useNavigate();
  const hits = useQuery(searchQueryOptions(query));
  if (!query.trim() || hits.isPending) return <Loader size="xs" m="sm" />;
  if (hits.isError)
    return (
      <Text size="sm" c="red" p="xs">
        {t("searchFailed")}
      </Text>
    );
  const items = hits.data ?? [];
  if (items.length === 0)
    return (
      <Text size="sm" c="dimmed" p="xs">
        {t("searchNothing")}
      </Text>
    );
  return (
    <Stack gap={4} role="list" aria-label={t("searchResults")}>
      {items.map((hit) => (
        <UnstyledButton
          key={hit.id}
          role="listitem"
          p="xs"
          style={{ borderRadius: "var(--mantine-radius-sm)" }}
          onClick={() => {
            onOpen?.();
            void navigate({ to: ARTICLE_ROUTE_PATH, params: { articleId: hit.id } });
          }}
        >
          <Text size="sm" fw={500} style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <ArticleIcon icon={hit.icon} size={14} />
            {hit.title || t("untitled")}
          </Text>
          {hit.path.length > 0 && (
            <Text size="xs" c="dimmed" truncate>
              {hit.path.map((item) => item.title || t("untitled")).join(" / ")}
            </Text>
          )}
          <Excerpt segments={hit.excerpt} />
        </UnstyledButton>
      ))}
    </Stack>
  );
};

import { Anchor, Group, Stack, Text, Title } from "@mantine/core";
import { IconFileText } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { backlinksQueryOptions } from "../api/files";
import { ARTICLE_ROUTE_PATH } from "../lib/routes";

/**
 * "Linked from" at the foot of a page (knowledge design K21): the pages that
 * link here and that the reader may read, with where each sits. Nothing shows
 * while there are none.
 */
export const Backlinks = ({ articleId }: { articleId: string }) => {
  const { t } = useI18n("knowledge");
  const backlinks = useQuery(backlinksQueryOptions(articleId));
  if (backlinks.isError)
    return (
      <Text size="xs" c="dimmed">
        {t("backlinksFailed")}
      </Text>
    );
  const items = backlinks.data ?? [];
  if (items.length === 0) return null;
  return (
    <Stack component="section" gap="xs" aria-labelledby={`backlinks-${articleId}`}>
      <Title order={2} size="h6" c="dimmed" id={`backlinks-${articleId}`}>
        {t("backlinksTitle")}
      </Title>
      {items.map((item) => (
        <Group key={item.id} gap="xs" wrap="nowrap">
          {item.icon ? <span aria-hidden>{item.icon}</span> : <IconFileText size={16} aria-hidden />}
          <Anchor
            size="sm"
            renderRoot={(props) => <Link to={ARTICLE_ROUTE_PATH} params={{ articleId: item.id }} {...props} />}
          >
            {item.title || t("untitled")}
          </Anchor>
          {item.path.length > 0 && (
            <Text size="xs" c="dimmed" truncate>
              {item.path.map((step) => step.title || t("untitled")).join(" / ")}
            </Text>
          )}
        </Group>
      ))}
    </Stack>
  );
};

import { Alert, Button, Group, Loader, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { reviewItemQueryOptions } from "../api/agents";
import { NotFoundError } from "../api/request";
import { ReviewItemCard } from "../components/review-item-card";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { AGENTS_HOME_ROUTE_PATH, AGENTS_REVIEW_ROUTE_PATH } from "../lib/routes";
import "../i18n";

/** One review item, where the internal note on the conversation links to (V20). */
export const ReviewItemPage = ({ itemId, access = noAgentsAccess }: { itemId: string; access?: AgentsAccess }) => {
  const { t } = useI18n("agents");
  const query = useQuery(reviewItemQueryOptions(itemId));
  const breadcrumbs = [
    { label: t("breadcrumbAgents"), to: AGENTS_HOME_ROUTE_PATH },
    { label: t("reviewTitle"), to: AGENTS_REVIEW_ROUTE_PATH },
  ];
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Stack gap="lg">
        <PageHeader title={t("reviewTitle")} breadcrumbs={breadcrumbs} />
        {query.error instanceof NotFoundError ? (
          <EmptyState title={t("itemNotFound")} />
        ) : (
          <Alert color="red">
            <Group>
              <Text size="sm">{t("reviewLoadFailed")}</Text>
              <Button size="xs" variant="light" onClick={() => void query.refetch()}>
                {t("tryAgain")}
              </Button>
            </Group>
          </Alert>
        )}
      </Stack>
    );
  return (
    <Stack gap="lg">
      <PageHeader title={t("itemTitle")} breadcrumbs={[...breadcrumbs, { label: t("itemTitle") }]} />
      {/* Keyed by the version, so a decision made elsewhere replaces what was typed. */}
      <ReviewItemCard key={query.data.version} item={query.data} canReview={access.canReview} />
    </Stack>
  );
};

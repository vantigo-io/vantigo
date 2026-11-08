import { Alert, Button, Group, Loader, Pagination, SegmentedControl, Select, Stack, Text } from "@mantine/core";
import { IconInbox } from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { agentsQueryOptions, REVIEW_PAGE_SIZE, reviewQueryOptions } from "../api/agents";
import { errorCode } from "../api/request";
import { ReviewItemCard } from "../components/review-item-card";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { AGENTS_HOME_ROUTE_PATH } from "../lib/routes";
import "../i18n";

type Which = "waiting" | "decided";

/**
 * The review queue across agents (design V20, screen 4): every reply and change
 * an agent left for a person, the ones that lapse soonest first, each with why
 * it waits, its score and sources, and the ways to decide it.
 */
export const ReviewPage = ({ access = noAgentsAccess }: { access?: AgentsAccess }) => {
  const { t } = useI18n("agents");
  const [which, setWhich] = useState<Which>("waiting");
  const [agentId, setAgentId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const agents = useQuery(agentsQueryOptions());
  // "Decided" is every state but waiting, filtered and paged on the server.
  const query = useQuery({
    ...reviewQueryOptions({
      state: which === "waiting" ? "waiting" : "decided",
      agentId: agentId ?? undefined,
      page: which === "decided" ? page : undefined,
    }),
    placeholderData: keepPreviousData,
  });
  const pending = query.isPending;
  const failed = query.isError ? query : undefined;
  const items = query.data?.items ?? [];
  const pages = Math.max(1, Math.ceil((query.data?.total ?? 0) / REVIEW_PAGE_SIZE));

  let body: ReactNode;
  if (pending) body = <Loader size="sm" />;
  else if (failed)
    body = (
      <Alert color="red">
        <Group>
          <Text size="sm">
            {errorCode(failed.error) === "reviewer_not_permitted" ? t("errorNotPermitted") : t("reviewLoadFailed")}
          </Text>
          <Button size="xs" variant="light" onClick={() => void failed.refetch()}>
            {t("tryAgain")}
          </Button>
        </Group>
      </Alert>
    );
  else if (items.length === 0)
    body = (
      <EmptyState
        icon={IconInbox}
        title={which === "waiting" ? t("queueEmpty") : t("decidedEmpty")}
        description={which === "waiting" ? t("queueEmptyDescription") : undefined}
      />
    );
  else
    body = (
      <Stack gap="md">
        {items.map((item) => (
          <ReviewItemCard key={item.id} item={item} canReview={access.canReview} />
        ))}
        {which === "decided" && pages > 1 && (
          <Pagination aria-label={t("queuePage")} total={pages} value={page} onChange={setPage} />
        )}
      </Stack>
    );

  return (
    <Stack gap="lg">
      <PageHeader
        title={t("reviewTitle")}
        description={t("reviewDescription")}
        breadcrumbs={[{ label: t("breadcrumbAgents"), to: AGENTS_HOME_ROUTE_PATH }, { label: t("reviewTitle") }]}
        actions={
          <Group gap="sm">
            <Select
              aria-label={t("allAgents")}
              placeholder={t("allAgents")}
              clearable
              data={(agents.data ?? []).map((agent) => ({ value: agent.id, label: agent.name }))}
              value={agentId}
              onChange={(value) => {
                setAgentId(value);
                setPage(1);
              }}
            />
            <SegmentedControl
              aria-label={t("reviewTitle")}
              value={which}
              onChange={(value) => {
                setWhich(value as Which);
                setPage(1);
              }}
              data={[
                { value: "waiting", label: t("queueWaiting") },
                { value: "decided", label: t("queueDecided") },
              ]}
            />
          </Group>
        }
      />
      {body}
    </Stack>
  );
};

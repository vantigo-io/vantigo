import { Alert, Button, Group, Loader, SegmentedControl, Select, Stack, Text } from "@mantine/core";
import { IconInbox } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { agentsQueryOptions, reviewQueryOptions } from "../api/agents";
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
  const agents = useQuery(agentsQueryOptions());
  // "Decided" is every state but waiting: the API lists the waiting ones first, so they are left out here.
  const query = useQuery(
    reviewQueryOptions({ state: which === "waiting" ? "waiting" : undefined, agentId: agentId ?? undefined }),
  );
  const pending = query.isPending;
  const failed = query.isError ? query : undefined;
  const items = (query.data?.items ?? []).filter((item) => (which === "waiting") === (item.state === "waiting"));

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
              onChange={setAgentId}
            />
            <SegmentedControl
              aria-label={t("reviewTitle")}
              value={which}
              onChange={(value) => setWhich(value as Which)}
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

import { Alert, Badge, Button, Code, Collapse, Group, List, Loader, Stack, Table, Text } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EmptyState, formatDate, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { agentQueryOptions, runQueryOptions } from "../api/agents";
import { NotFoundError } from "../api/request";
import type { AgentRunAction, AgentRunDetail } from "../api/types";
import { ReviewItemCard } from "../components/review-item-card";
import { type AgentsAccess, noAgentsAccess } from "../lib/access";
import { heldKey } from "../lib/review";
import { agentLinkOptions } from "../lib/routes";
import "../i18n";

const time = (value: string) => formatDate(value, { timeStyle: "medium" });

/** What the audit says of a call: who made it, how, and who approved it. */
const AuditLine = ({ action, agentName }: { action: AgentRunAction; agentName: string }) => {
  const { t } = useI18n("agents");
  return (
    <List.Item>
      <Text size="sm">
        <Code>{action.operationId}</Code>{" "}
        {action.ref !== action.operationId ? `${action.ref.replace(action.operationId, "").trim()} ` : ""}
        {action.httpStatus ? `· ${action.httpStatus} ` : ""}
        {action.approvedByUserId ? t("auditApproved", { agent: agentName }) : t("auditBy", { agent: agentName })}
        {action.scoped && (
          <Badge ml={6} size="xs" variant="light">
            {t("auditCustomerData")}
          </Badge>
        )}
      </Text>
    </List.Item>
  );
};

const Verdict = ({ verdict }: { verdict: NonNullable<AgentRunDetail["verdict"]> }) => {
  const { t } = useI18n("agents");
  const held = Array.isArray(verdict.held) ? (verdict.held as string[]) : [];
  const refused = Array.isArray(verdict.refused) ? (verdict.refused as string[]) : [];
  const sources = Array.isArray(verdict.sources) ? (verdict.sources as string[]) : [];
  return (
    <Stack gap={4} data-testid="verdict">
      <Text size="sm">
        {t("verdictLine", {
          score: String(verdict.score ?? "—"),
          modelScore: String(verdict.modelScore ?? "—"),
          sources: sources.length,
        })}
      </Text>
      <Text size="sm">
        {t("verdictMode", {
          mode: t(`replyMode.${String(verdict.mode)}`, { threshold: String(verdict.threshold ?? "") }),
          decision: verdict.sent ? t("verdictSend") : refused.length > 0 ? t("verdictRefused") : t("verdictQueue"),
        })}
      </Text>
      {(held.length > 0 || refused.length > 0) && (
        <List size="sm" spacing={2}>
          {[...refused, ...held].map((code) => (
            <List.Item key={code}>{t(heldKey(code))}</List.Item>
          ))}
        </List>
      )}
    </Stack>
  );
};

/**
 * A run (design V25, screen 6): the timeline of its tool calls, the verdict and
 * the mode's decision, what the audit says and the items it left. The
 * transcript, which holds the customer's messages, is folded away. It needs
 * agents:activity.
 */
export const RunPage = ({ runId, access = noAgentsAccess }: { runId: string; access?: AgentsAccess }) => {
  const { t } = useI18n("agents");
  const query = useQuery({ ...runQueryOptions(runId), enabled: access.canSeeActivity });
  const agent = useQuery({ ...agentQueryOptions(query.data?.run.agentId ?? ""), enabled: Boolean(query.data) });
  const [transcript, { toggle }] = useDisclosure(false);
  const breadcrumbs = [{ label: t("breadcrumbAgents"), to: "/agents" }];

  if (!access.canSeeActivity)
    return (
      <Stack gap="lg">
        <PageHeader title={t("runTitle")} breadcrumbs={breadcrumbs} />
        <Alert color="gray">{t("noActivityPermission")}</Alert>
      </Stack>
    );
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Stack gap="lg">
        <PageHeader title={t("runTitle")} breadcrumbs={breadcrumbs} />
        {query.error instanceof NotFoundError ? (
          <EmptyState title={t("runNotFound")} />
        ) : (
          <Alert color="red">
            <Group>
              <Text size="sm">{t("activityLoadFailed")}</Text>
              <Button size="xs" variant="light" onClick={() => void query.refetch()}>
                {t("tryAgain")}
              </Button>
            </Group>
          </Alert>
        )}
      </Stack>
    );

  const { run, actions, items, verdict, transcript: messages } = query.data;
  const agentName = agent.data?.name ?? t("theAgent");
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("runHeading", { id: run.id.slice(0, 4) })}
        breadcrumbs={[
          ...breadcrumbs,
          { label: agentName, to: `/agents/${run.agentId}?tab=activity` },
          { label: t("runTitle") },
        ]}
        actions={<Badge variant="light">{t(`runState.${run.state}`)}</Badge>}
      />
      <Text size="sm" c="dimmed">
        {t("runFigures", { calls: run.toolCalls, turns: run.modelTurns, tokens: run.inputTokens + run.outputTokens })}
        {" · "}
        {formatDate(run.startedAt, { dateStyle: "medium", timeStyle: "short" })}
        {" · "}
        <Link {...agentLinkOptions(run.agentId, "activity")}>{agentName}</Link>
      </Text>
      {run.summary && <Alert color="gray">{run.summary}</Alert>}

      <PageSection title={t("timeline")}>
        {actions.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("timelineEmpty")}
          </Text>
        ) : (
          <Table withRowBorders={false} data-testid="timeline">
            <Table.Tbody>
              {actions.map((action) => (
                <Table.Tr key={action.id}>
                  <Table.Td w={100}>
                    <Text size="xs" c="dimmed">
                      {time(action.createdAt)}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Code>{action.operationId}</Code>{" "}
                    <Text span size="xs" c="dimmed">
                      {action.ref !== action.operationId ? action.ref.replace(action.operationId, "").trim() : ""}
                    </Text>
                  </Table.Td>
                  <Table.Td w={90}>
                    <Badge size="xs" variant="light" color={action.status === "ok" ? "teal" : "yellow"}>
                      {action.httpStatus ?? action.status}
                    </Badge>
                  </Table.Td>
                  <Table.Td w={90}>
                    <Text size="xs" c="dimmed">
                      {t("durationMs", { ms: action.durationMs })}
                    </Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </PageSection>

      {verdict && (
        <PageSection title={t("verdictTitle")}>
          <Verdict verdict={verdict} />
        </PageSection>
      )}

      <PageSection title={t("auditTitle")}>
        {actions.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("timelineEmpty")}
          </Text>
        ) : (
          <List spacing={4} data-testid="audit-lines">
            {actions.map((action) => (
              <AuditLine key={action.id} action={action} agentName={agentName} />
            ))}
          </List>
        )}
        <Text size="xs" c="dimmed" mt="xs">
          {t("auditKept")}
        </Text>
      </PageSection>

      {items.length > 0 && (
        <PageSection title={t("runItems")}>
          <Stack gap="md">
            {items.map((item) => (
              <ReviewItemCard key={item.id} item={item} canReview={access.canReview} />
            ))}
          </Stack>
        </PageSection>
      )}

      <PageSection title={t("transcriptTitle")}>
        <Button variant="subtle" size="compact-sm" onClick={toggle}>
          {transcript ? t("transcriptHide") : t("transcriptShow", { count: messages.length })}
        </Button>
        <Collapse expanded={transcript}>
          <Stack gap="xs" mt="xs" data-testid="transcript">
            {messages.map((message) => (
              <div key={message.id}>
                <Text size="xs" c="dimmed">
                  {message.role} · {time(message.createdAt)}
                </Text>
                <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                  {message.text || (message.toolCalls ? JSON.stringify(message.toolCalls) : "")}
                </Text>
              </div>
            ))}
          </Stack>
        </Collapse>
      </PageSection>
    </Stack>
  );
};

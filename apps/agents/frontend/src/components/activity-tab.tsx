import { Alert, Anchor, Badge, Button, Code, Group, Loader, Stack, Table, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { formatDate, useI18n } from "@vantigo/frontend-shell";
import { auditQueryOptions, runsQueryOptions } from "../api/agents";
import type { Agent } from "../api/types";
import { runLinkOptions } from "../lib/routes";

/** The Owner's changes to the settings (V25): who changed which agent, role, mode, level or model. */
const AuditList = () => {
  const { t } = useI18n("agents");
  const query = useQuery(auditQueryOptions());
  if (query.isPending) return <Loader size="sm" />;
  if (query.isError || !query.data)
    return (
      <Text size="sm" c="dimmed">
        {t("auditLoadFailed")}
      </Text>
    );
  if (query.data.events.length === 0)
    return (
      <Text size="sm" c="dimmed">
        {t("auditEmpty")}
      </Text>
    );
  return (
    <Table data-testid="audit-events">
      <Table.Tbody>
        {query.data.events.map((event) => (
          <Table.Tr key={event.id}>
            <Table.Td w={170}>
              <Text size="xs" c="dimmed">
                {formatDate(event.at, { dateStyle: "medium", timeStyle: "short" })}
              </Text>
            </Table.Td>
            <Table.Td>
              <Code>{event.action}</Code>
            </Table.Td>
            <Table.Td>
              <Text size="xs" c="dimmed" lineClamp={1}>
                {event.target ?? ""}
              </Text>
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
};

/** The agent's runs (V25, screen 6): newest first, each opening its timeline. Needs agents:activity. */
export const ActivityTab = ({ agent, canSeeActivity }: { agent: Agent; canSeeActivity: boolean }) => {
  const { t } = useI18n("agents");
  const query = useQuery({ ...runsQueryOptions(agent.id), enabled: canSeeActivity });
  return (
    <Stack gap="lg">
      {!canSeeActivity ? (
        <Alert color="gray">{t("noActivityPermission")}</Alert>
      ) : query.isPending ? (
        <Loader size="sm" />
      ) : query.isError || !query.data ? (
        <Alert color="red">
          <Group>
            <Text size="sm">{t("activityLoadFailed")}</Text>
            <Button size="xs" variant="light" onClick={() => void query.refetch()}>
              {t("tryAgain")}
            </Button>
          </Group>
        </Alert>
      ) : query.data.runs.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("runsEmpty")}
        </Text>
      ) : (
        <Table highlightOnHover data-testid="runs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("runStarted")}</Table.Th>
              <Table.Th>{t("runStateLabel")}</Table.Th>
              <Table.Th>{t("runOutcome")}</Table.Th>
              <Table.Th ta="right">{t("usageTokens")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {query.data.runs.map((run) => (
              <Table.Tr key={run.id}>
                <Table.Td>{formatDate(run.startedAt, { dateStyle: "medium", timeStyle: "short" })}</Table.Td>
                <Table.Td>
                  <Badge
                    variant="light"
                    color={run.state === "failed" ? "red" : run.state === "handed_off" ? "yellow" : "gray"}
                  >
                    {t(`runState.${run.state}`)}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{run.outcome ? t(`runOutcomes.${run.outcome}`) : "—"}</Text>
                </Table.Td>
                <Table.Td ta="right">{run.inputTokens + run.outputTokens}</Table.Td>
                <Table.Td ta="right">
                  <Anchor size="sm" renderRoot={(props) => <Link {...runLinkOptions(run.id)} {...props} />}>
                    {t("openRun")}
                  </Anchor>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      <Stack gap="xs">
        <Text fw={500}>{t("auditTitleSettings")}</Text>
        <AuditList />
      </Stack>
    </Stack>
  );
};

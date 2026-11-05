import { Badge, Card, Group, Stack, Text, Timeline, Title } from "@mantine/core";
import type { Change, Line } from "../api/types";
import { hasWords } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

/**
 * The order's change log (Endringslogg, O26, O32), newest first: who, when,
 * at which version, and each typed action with its before and after. A line
 * an action names is told by its name on the order today.
 */
export const ChangeLog = ({ changes, lines }: { changes: readonly Change[]; lines: readonly Line[] }) => {
  const { t, dateTime } = useOrdersFormat();
  const lineName = (id?: number | null) => {
    if (id == null) return undefined;
    const line = lines.find((l) => l.id === id);
    return line ? (line.name ?? line.description) || line.sku || `#${id}` : `#${id}`;
  };
  const newest = [...changes].reverse();
  return (
    <Card withBorder padding="lg" data-testid="change-log">
      <Stack gap="md">
        <Title order={4}>{t("changeLog")}</Title>
        {newest.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noChanges")}
          </Text>
        ) : (
          <Timeline bulletSize={14} lineWidth={2} active={newest.length}>
            {newest.map((change) => (
              <Timeline.Item
                key={change.id}
                title={
                  <Group gap="xs">
                    <Text size="sm" fw={600}>
                      {change.actor.displayName}
                    </Text>
                    <Badge size="xs" variant="outline">
                      {t("versionNumber", { version: change.version })}
                    </Badge>
                  </Group>
                }
              >
                <Text size="xs" c="dimmed">
                  {dateTime(change.at)}
                </Text>
                <Stack gap={2} mt={4}>
                  {change.actions.map((action, index) => {
                    const words = hasWords(`changeAction.${action.kind}`)
                      ? t(`changeAction.${action.kind}`)
                      : action.kind;
                    const line = lineName(action.lineId);
                    // A final period's values are words of their own (owner, 2026-11-06).
                    const value = (v?: string | null) =>
                      v != null && action.kind === "final_period" && hasWords(`finalPeriod.${v}`)
                        ? t(`finalPeriod.${v}`)
                        : v;
                    return (
                      <Text size="sm" key={index}>
                        {words}
                        {line && (
                          <Text span c="dimmed">
                            {" "}
                            · {line}
                          </Text>
                        )}
                        {(action.from != null || action.to != null) && (
                          <Text span ff="monospace" size="xs" ml={6}>
                            {value(action.from) ?? "—"} → {value(action.to) ?? "—"}
                          </Text>
                        )}
                      </Text>
                    );
                  })}
                </Stack>
              </Timeline.Item>
            ))}
          </Timeline>
        )}
      </Stack>
    </Card>
  );
};

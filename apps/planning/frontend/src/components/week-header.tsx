import { Stack, Text } from "@mantine/core";
import { usePlanningFormat } from "../lib/format";
import { toUtc } from "../lib/weeks";

/** A week column's heading: its number and its Monday, the whole week said to a screen reader. */
export const WeekHeader = ({ monday, current }: { monday: string; current?: boolean }) => {
  const { t, week, formatters } = usePlanningFormat();
  const date = formatters.formatDate(toUtc(monday), { day: "numeric", month: "short", timeZone: "UTC" });
  return (
    <Stack gap={0} aria-label={t("weekOf", { week: week(monday), date })}>
      <Text size="xs" fw={current ? 700 : 600} c={current ? "blue" : undefined}>
        {week(monday)}
      </Text>
      <Text size="xs" c="dimmed">
        {date}
      </Text>
    </Stack>
  );
};

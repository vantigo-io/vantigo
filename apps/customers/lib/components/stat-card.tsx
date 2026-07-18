import { Group, Paper, Text } from "@mantine/core";
import { IconTrendingDown, IconTrendingUp } from "@tabler/icons-react";

export interface StatCardProps {
  /** Metric name, already translated by the caller */
  label: string;
  /** Pre-formatted value (caller handles number/currency/locale formatting) */
  value: string;
  /** Percentage change vs previous period; sign determines color and icon */
  diff?: number;
  /** Optional context line, already translated */
  description?: string;
}

/**
 * Reusable dashboard statistic card.
 *
 * Purely presentational: all strings and number formatting are the
 * caller's responsibility, keeping the card i18n- and currency-agnostic.
 */
export function StatCard({ label, value, diff, description }: Readonly<StatCardProps>) {
  const isPositive = diff !== undefined && diff >= 0;
  const DiffIcon = isPositive ? IconTrendingUp : IconTrendingDown;

  return (
    <Paper withBorder p="md" radius="md">
      <Group justify="space-between" wrap="nowrap">
        <Text size="xs" c="dimmed" fw={700} tt="uppercase" truncate>
          {label}
        </Text>
        {diff !== undefined && (
          <Group gap={4} wrap="nowrap" c={isPositive ? "teal" : "red"}>
            <Text size="sm" fw={500}>
              {`${diff >= 0 ? "+" : ""}${diff.toFixed(1)}%`}
            </Text>
            <DiffIcon size={16} stroke={1.5} />
          </Group>
        )}
      </Group>

      <Text fz={28} fw={700} mt="xs" truncate>
        {value}
      </Text>

      {description && (
        <Text size="xs" c="dimmed" mt={4}>
          {description}
        </Text>
      )}
    </Paper>
  );
}

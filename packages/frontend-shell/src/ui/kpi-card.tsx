import { Sparkline } from "@mantine/charts";
import { Card, Skeleton, Stack, Text } from "@mantine/core";
import type { ReactNode } from "react";

export interface KpiCardDelta {
  value: number;
  label?: string;
}

export interface KpiCardProps {
  label: string;
  value: ReactNode;
  hint?: string;
  delta?: KpiCardDelta;
  sparklineData?: number[];
  href?: string;
  loading?: boolean;
}

const formatDelta = (value: number) => `${value > 0 ? "+" : ""}${value}%`;

const deltaColor = (value: number) => {
  if (value > 0) return "green";
  if (value < 0) return "red";
  return "gray";
};

export const KpiCard = ({ label, value, hint, delta, sparklineData, href, loading = false }: KpiCardProps) => {
  const cardProps = href ? { component: "a" as const, href, style: { color: "inherit", textDecoration: "none" } } : {};

  return (
    <Card withBorder padding="md" radius="lg" mih={154} {...cardProps}>
      {loading ? (
        <Stack gap="xs">
          <Skeleton h={14} w="45%" />
          <Skeleton h={30} w="60%" />
          {(hint || delta) && <Skeleton h={12} w="50%" />}
          {sparklineData !== undefined && <Skeleton h={32} mt="xs" />}
        </Stack>
      ) : (
        <Stack gap={2}>
          <Text size="sm" c="dimmed">
            {label}
          </Text>
          <Text size="xl" fw={700}>
            {value}
          </Text>
          {delta && (
            <Text size="xs" fw={500} c={deltaColor(delta.value)}>
              {formatDelta(delta.value)}
              {delta.label && (
                <Text span c="dimmed" fw={400}>
                  {` ${delta.label}`}
                </Text>
              )}
            </Text>
          )}
          {hint && (
            <Text size="xs" c="dimmed">
              {hint}
            </Text>
          )}
          {sparklineData !== undefined && (
            <Sparkline data={sparklineData} h={32} mt="sm" w="100%" withGradient={false} />
          )}
        </Stack>
      )}
    </Card>
  );
};

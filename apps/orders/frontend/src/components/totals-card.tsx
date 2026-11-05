import { Card, Divider, Group, Stack, Text } from "@mantine/core";
import type { Totals } from "../api/types";
import { useOrdersFormat } from "../lib/format";

export interface TotalsCardProps {
  totals: Totals;
  currency: string;
  /** Whether the lines changed since the totals were computed: they are then the last save's. */
  stale?: boolean;
  /** A quote's optional lines are left out of its totals (O21). */
  hasOptional?: boolean;
}

/**
 * The document's totals as the server computed them (O17): the net, VAT per
 * category and rate on the sum of the nets, and the total. Nothing here is
 * added up in the browser.
 */
export const TotalsCard = ({ totals, currency, stale = false, hasOptional = false }: TotalsCardProps) => {
  const { t, money, percent } = useOrdersFormat();
  return (
    <Card
      withBorder
      padding="lg"
      maw={420}
      ml="auto"
      w="100%"
      data-testid="totals"
      style={{ opacity: stale ? 0.6 : 1 }}
    >
      <Stack gap={6}>
        <Group justify="space-between">
          <Text size="sm" c="dimmed">
            {t("totalNet")}
          </Text>
          <Text size="sm" ff="monospace">
            {money(totals.net, currency)}
          </Text>
        </Group>
        {totals.vatSummaries.map((summary) => (
          <Group justify="space-between" key={`${summary.category}-${summary.ratePercent}`}>
            <Text size="sm" c="dimmed">
              {t("vatAtRate", { rate: percent(summary.ratePercent), base: money(summary.taxableAmount, currency) })}
            </Text>
            <Text size="sm" ff="monospace">
              {money(summary.vatAmount, currency)}
            </Text>
          </Group>
        ))}
        <Divider />
        <Group justify="space-between" align="baseline">
          <Text fw={700}>{t("totalGross")}</Text>
          <Text fw={800} size="xl" ff="monospace" data-testid="grand-total">
            {money(totals.gross, currency)}
          </Text>
        </Group>
        {hasOptional && (
          <Text size="xs" c="violet">
            {t("optionalLeftOut")}
          </Text>
        )}
        {stale && (
          <Text size="xs" c="orange" role="status">
            {t("totalsStale")}
          </Text>
        )}
      </Stack>
    </Card>
  );
};

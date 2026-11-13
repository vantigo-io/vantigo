import { Badge, Group, List, Stack, Table, Text } from "@mantine/core";
import { PageSection } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { useManufacturingFormat } from "../lib/format";

type ManufacturingOrderCost = components["schemas"]["ManufacturingOrderCost"];

/** The parts a cost is split in (M39, M40), in the panel's order. */
const costParts = ["components", "machine", "labour", "total"] as const;

/** Whether decimal text is below zero: under the estimate. Read as text, never as a float. */
const isNegative = (text: string) => text.trim().startsWith("-");
const isZero = (text: string) => !/[1-9]/.test(text);

export interface CostPanelProps {
  cost: ManufacturingOrderCost;
  currency?: string;
}

/**
 * An order's cost (M39–M41): the estimate fixed at confirm, the actual —
 * running while the order is open, fixed at close — and their deviation per
 * part in amount and per cent; the cost per unit once closed; a note where a
 * part counted a missing standard cost or rate as 0; and the downtime by
 * reason, listed and never charged. The page shows it only when the server
 * answered `cost`, which it leaves out without `manufacturing:costs-view` (M38).
 */
export const CostPanel = ({ cost, currency = "NOK" }: CostPanelProps) => {
  const { t, money, decimal, minutes } = useManufacturingFormat();
  const percent = (text: string | null) => (text === null ? "—" : `${decimal(text)} %`);
  const deviationColor = (text: string) => (isZero(text) ? undefined : isNegative(text) ? "teal" : "red");

  return (
    <PageSection
      title={t("cost")}
      actions={
        cost.actual && (
          <Badge variant="light" color={cost.actualFixed ? "teal" : "blue"}>
            {cost.actualFixed ? t("costActualFixed") : t("costActualRunning")}
          </Badge>
        )
      }
    >
      <Stack gap="md" data-assist="manufacturing.order.cost">
        {!cost.estimate && !cost.actual ? (
          <Text size="sm" c="dimmed">
            {t("costNoneOnDraft")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={560}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("costPart")}</Table.Th>
                  <Table.Th ta="right">{t("costEstimate")}</Table.Th>
                  <Table.Th ta="right">{t("costActual")}</Table.Th>
                  <Table.Th ta="right">{t("costDeviation")}</Table.Th>
                  <Table.Th ta="right">{t("costDeviationPercent")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {costParts.map((part) => {
                  const deviation = cost.deviation?.[part];
                  return (
                    <Table.Tr key={part} fw={part === "total" ? 600 : undefined}>
                      <Table.Td>{t(`costPart_${part}`)}</Table.Td>
                      <Table.Td ta="right">{cost.estimate ? money(cost.estimate[part], currency) : "—"}</Table.Td>
                      <Table.Td ta="right">{cost.actual ? money(cost.actual[part], currency) : "—"}</Table.Td>
                      <Table.Td ta="right" c={deviation ? deviationColor(deviation.amount) : undefined}>
                        {deviation ? money(deviation.amount, currency) : "—"}
                      </Table.Td>
                      <Table.Td ta="right" c={deviation ? deviationColor(deviation.amount) : undefined}>
                        {deviation ? percent(deviation.percent) : "—"}
                      </Table.Td>
                    </Table.Tr>
                  );
                })}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
        {(cost.estimate?.incomplete || cost.actual?.incomplete) && (
          <Stack gap={2}>
            {cost.estimate?.incomplete && (
              <Text size="sm" c="orange">
                {t("costEstimateIncomplete")}
              </Text>
            )}
            {cost.actual?.incomplete && (
              <Text size="sm" c="orange">
                {t("costActualIncomplete")}
              </Text>
            )}
          </Stack>
        )}
        {(cost.byProducts ?? []).length > 0 && (
          <Stack gap={4} data-assist="manufacturing.order.cost-split">
            <Text size="sm" fw={500}>
              {t("costSplit")}
            </Text>
            <Table.ScrollContainer minWidth={560}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("byProduct")}</Table.Th>
                    <Table.Th ta="right">{t("costShare")}</Table.Th>
                    <Table.Th ta="right">{t("costEstimate")}</Table.Th>
                    <Table.Th ta="right">{t("costActual")}</Table.Th>
                    <Table.Th ta="right">{t("costPerUnit")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {cost.byProducts.map((part) => (
                    <Table.Tr key={part.outputId}>
                      <Table.Td>{part.variant.sku}</Table.Td>
                      <Table.Td ta="right">{`${decimal(part.costShare)} %`}</Table.Td>
                      <Table.Td ta="right">{part.estimate ? money(part.estimate, currency) : "—"}</Table.Td>
                      <Table.Td ta="right">{part.actual ? money(part.actual, currency) : "—"}</Table.Td>
                      <Table.Td ta="right">
                        {part.notProduced ? (
                          <Text size="sm" c="dimmed" component="span">
                            {t("notProduced")}
                          </Text>
                        ) : part.costPerUnit ? (
                          money(part.costPerUnit, currency)
                        ) : (
                          "—"
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                  {cost.mainActual && (
                    <Table.Tr fw={600}>
                      <Table.Td>{t("mainProduct")}</Table.Td>
                      <Table.Td />
                      <Table.Td />
                      <Table.Td ta="right">{money(cost.mainActual, currency)}</Table.Td>
                      <Table.Td ta="right">{cost.costPerUnit ? money(cost.costPerUnit, currency) : "—"}</Table.Td>
                    </Table.Tr>
                  )}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
            {cost.byProducts.some((part) => part.notProduced) && (
              <Text size="xs" c="dimmed">
                {t("notProducedHint")}
              </Text>
            )}
          </Stack>
        )}
        {cost.costPerUnit !== null && (
          <Group gap="xs">
            <Text size="sm" c="dimmed">
              {t("costPerUnit")}
            </Text>
            <Text size="sm" fw={600}>
              {money(cost.costPerUnit, currency)}
            </Text>
          </Group>
        )}
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("downtimeByReason")}
          </Text>
          {cost.downtime.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noDowntime")}
            </Text>
          ) : (
            <List size="sm">
              {cost.downtime.map((entry) => (
                <List.Item key={entry.reason}>
                  {t("downtimeEntry", { reason: t(`reason_${entry.reason}`), minutes: minutes(entry.minutes) })}
                </List.Item>
              ))}
            </List>
          )}
        </Stack>
      </Stack>
    </PageSection>
  );
};

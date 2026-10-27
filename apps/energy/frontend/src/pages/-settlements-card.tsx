import { Anchor, Badge, Button, Card, Group, Loader, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { Fragment, useState } from "react";
import { type Settlement, settlementsQueryOptions } from "../api/settlements";
import { MARKET_TIME_ZONE } from "../lib/market-time";
import "../i18n";

/**
 * The metering point's **Settlements** card (energy consumption billing design
 * D11): each billing period with its state, kWh, amount, a blocker in words and
 * the invoice number as a link. The page shows it to a caller holding
 * `energy:supply-periods-view` and `energy:consumption-view`.
 */
export const SettlementsCard = ({ meteringPointId }: { meteringPointId: number }) => {
  const { t, formatters } = useI18n("energy");
  const { data, isPending, isError } = useQuery(settlementsQueryOptions(meteringPointId));
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set());
  const day = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeZone: MARKET_TIME_ZONE });
  // The end is exclusive: the last day of the period is the one the instant before it falls in.
  const period = (settlement: Settlement) =>
    `${day(settlement.periodStart)} – ${day(new Date(Date.parse(settlement.periodEnd) - 1).toISOString())}`;
  const toggle = (index: number) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(index)) next.add(index);
      return next;
    });
  return (
    <Card withBorder>
      <Stack>
        <Title order={3}>{t("settlements")}</Title>
        {isPending ? (
          <Loader size="sm" aria-label={t("loadingSettlements")} />
        ) : isError ? (
          <Text c="red">{t("failedToLoadSettlements")}</Text>
        ) : data.length === 0 ? (
          <EmptyState size="sm" title={t("noSettlements")} />
        ) : (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("settlementPeriod")}</Table.Th>
                <Table.Th>{t("settlementKind")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th ta="right">{t("settlementKwh")}</Table.Th>
                <Table.Th ta="right">{t("settlementAmount")}</Table.Th>
                <Table.Th>{t("settlementInvoice")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {data.map((settlement, index) => {
                const open = expanded.has(index);
                return (
                  <Fragment key={settlement.id ?? `${settlement.supplyPeriodId}-${settlement.periodStart}`}>
                    <Table.Tr>
                      <Table.Td>{period(settlement)}</Table.Td>
                      <Table.Td>
                        {settlement.kind === "correction" ? (
                          <Badge color="orange" variant="light">
                            {t("settlementKindCorrection")}
                          </Badge>
                        ) : (
                          t("settlementKindRegular")
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Group gap="xs">
                          <Badge color={settlement.state === "invoiced" ? "teal" : "gray"}>
                            {t(`settlementState_${settlement.state}`)}
                          </Badge>
                          {settlement.estimated && (
                            <Badge color="yellow" variant="light">
                              {t("settlementEstimated")}
                            </Badge>
                          )}
                        </Group>
                        {settlement.blocked && (
                          <Text size="xs" c="red">
                            {t(`settlementBlocked_${settlement.blocked}`)}
                            {settlement.blockedDetail ? `: ${settlement.blockedDetail}` : ""}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td ta="right">
                        {formatters.formatNumber(settlement.kwh, { maximumFractionDigits: 3 })}
                      </Table.Td>
                      <Table.Td ta="right">
                        {formatters.formatCurrency(settlement.amount, settlement.currency)}
                      </Table.Td>
                      <Table.Td>
                        {settlement.invoice && (
                          <Anchor component={Link} to={`/invoices/${settlement.invoice.id}` as never} size="sm">
                            {settlement.invoice.number}
                          </Anchor>
                        )}
                      </Table.Td>
                      <Table.Td>
                        {settlement.lines.length > 0 && (
                          <Button size="compact-sm" variant="subtle" aria-expanded={open} onClick={() => toggle(index)}>
                            {open ? t("settlementHideLines") : t("settlementShowLines")}
                          </Button>
                        )}
                      </Table.Td>
                    </Table.Tr>
                    {open && (
                      <Table.Tr>
                        <Table.Td colSpan={7}>
                          <Table>
                            <Table.Thead>
                              <Table.Tr>
                                <Table.Th>{t("settlementLineText")}</Table.Th>
                                <Table.Th ta="right">{t("settlementLineQuantity")}</Table.Th>
                                <Table.Th ta="right">{t("settlementLineUnitPrice")}</Table.Th>
                                <Table.Th ta="right">{t("settlementAmount")}</Table.Th>
                              </Table.Tr>
                            </Table.Thead>
                            <Table.Tbody>
                              {settlement.lines.map((line, lineIndex) => (
                                <Table.Tr key={lineIndex}>
                                  <Table.Td>
                                    {line.text}{" "}
                                    <Text span size="xs" c="dimmed">
                                      ({t(`settlementLineKind_${line.kind}`)})
                                    </Text>
                                  </Table.Td>
                                  <Table.Td ta="right">
                                    {formatters.formatNumber(line.quantity, { maximumFractionDigits: 3 })}{" "}
                                    {t(`settlementUnit_${line.unit}`)}
                                  </Table.Td>
                                  <Table.Td ta="right">
                                    {formatters.formatNumber(line.unitPrice, { maximumFractionDigits: 6 })}
                                  </Table.Td>
                                  <Table.Td ta="right">
                                    {formatters.formatCurrency(line.amount, settlement.currency)}
                                  </Table.Td>
                                </Table.Tr>
                              ))}
                            </Table.Tbody>
                          </Table>
                        </Table.Td>
                      </Table.Tr>
                    )}
                  </Fragment>
                );
              })}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};

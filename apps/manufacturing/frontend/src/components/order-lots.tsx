import { Alert, Anchor, Badge, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { type ManufacturingOrder, orderLotsQueryOptions } from "../api/orders";
import { refusalMessage, useManufacturingFormat } from "../lib/format";

/**
 * A lot's number linked to its page in Inventory — its id when no number was kept — and *Trace*, which opens
 * where the lot went: who received the lot made, and what else the lot consumed went into. Both go through the
 * host's router (no reload, under the deployment's base path), and only for a caller who may open them there
 * (the meta's `canTraceLots`: Inventory keeps stock and the caller holds both of its views); otherwise the
 * number alone.
 */
const LotLink = ({ lotId, number, canTrace }: { lotId: number; number: string; canTrace: boolean }) => {
  const { t } = useManufacturingFormat();
  if (!canTrace) return <Text size="sm">{number || `#${lotId}`}</Text>;
  return (
    <Group gap="xs" wrap="nowrap" component="span">
      <Anchor size="sm" renderRoot={(props) => <Link to={`/inventory/lots/${lotId}` as never} {...props} />}>
        {number || `#${lotId}`}
      </Anchor>
      <Anchor
        size="xs"
        c="dimmed"
        renderRoot={(props) => <Link to={"/inventory/trace" as never} search={{ lotId } as never} {...props} />}
        aria-label={t("traceLotOf", { number: number || `#${lotId}` })}
      >
        {t("traceLot")}
      </Anchor>
    </Group>
  );
};

/**
 * The order's lots (manufacturing phase 2 M83), on the order page's Lots tab
 * and the work order's page: the lots its productions put the output in, and
 * per component line the lots they took, each linked to its page in
 * Inventory and to its trace there (M94) when the caller may open them.
 */
export const OrderLots = ({ order }: { order: Pick<ManufacturingOrder, "id"> & { variant: { unit: string } } }) => {
  const { t, quantity, day } = useManufacturingFormat();
  const lots = useQuery(orderLotsQueryOptions(order.id));
  const canTrace = useQuery(manufacturingMetaQueryOptions()).data?.canTraceLots ?? false;
  if (lots.isPending) return <ContentSkeleton />;
  if (lots.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, lots.error)}
      </Alert>
    );
  const { outputs, components } = lots.data;
  const byProducts = lots.data.byProducts ?? [];
  if (outputs.length === 0 && components.length === 0 && byProducts.length === 0)
    return <Text c="dimmed">{t("noLots")}</Text>;
  return (
    <Stack gap="lg">
      {outputs.length > 0 && (
        <Stack gap="xs">
          <Title order={5}>{t("outputLots")}</Title>
          <Table.ScrollContainer minWidth={420}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("lotColumn")}</Table.Th>
                  <Table.Th>{t("lotQuantity")}</Table.Th>
                  <Table.Th>{t("lotProductions")}</Table.Th>
                  <Table.Th>{t("lotUnbuilt")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {outputs.map((lot) => (
                  <Table.Tr key={lot.lotId}>
                    <Table.Td>
                      <LotLink lotId={lot.lotId} number={lot.lotNumber} canTrace={canTrace} />
                      {lot.serial && (
                        <Badge ml="xs" size="xs" variant="light">
                          {t("serialBadge")}
                        </Badge>
                      )}
                    </Table.Td>
                    <Table.Td>{quantity(lot.quantity, order.variant.unit)}</Table.Td>
                    <Table.Td>{lot.productions}</Table.Td>
                    <Table.Td>{/[1-9]/.test(lot.unbuilt) ? quantity(lot.unbuilt, order.variant.unit) : "—"}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
      )}
      {byProducts.length > 0 && (
        <Stack gap="xs">
          <Title order={5}>{t("byProductLots")}</Title>
          <Table.ScrollContainer minWidth={420}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("byProduct")}</Table.Th>
                  <Table.Th>{t("lotColumn")}</Table.Th>
                  <Table.Th>{t("lotQuantity")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {byProducts.map((row, index) => (
                  // A by-product's lot may take several productions' output: each row is one production's.
                  <Table.Tr key={`${row.outputId}-${row.lotId ?? 0}-${index}`}>
                    <Table.Td>{row.variant.sku}</Table.Td>
                    <Table.Td>
                      {row.lotId !== null && (
                        <LotLink lotId={row.lotId} number={row.lotNumber ?? ""} canTrace={canTrace} />
                      )}
                    </Table.Td>
                    <Table.Td>{quantity(row.quantity, row.variant.unit)}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
      )}
      {components.length > 0 && (
        <Stack gap="xs">
          <Title order={5}>{t("consumedLots")}</Title>
          <Table.ScrollContainer minWidth={560}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("component")}</Table.Th>
                  <Table.Th>{t("lotColumn")}</Table.Th>
                  <Table.Th>{t("lotExpiry")}</Table.Th>
                  <Table.Th>{t("consumedColumn")}</Table.Th>
                  <Table.Th>{t("scrapColumn")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {components.flatMap((line) =>
                  line.lots.map((lot, index) => (
                    <Table.Tr key={`${line.componentId}-${lot.lotId}`}>
                      <Table.Td>{index === 0 ? line.variant.sku : ""}</Table.Td>
                      <Table.Td>
                        <LotLink lotId={lot.lotId} number={lot.lotNumber} canTrace={canTrace} />
                      </Table.Td>
                      <Table.Td>{lot.expiry ? day(lot.expiry) : ""}</Table.Td>
                      <Table.Td>{quantity(lot.consumed, line.variant.unit)}</Table.Td>
                      <Table.Td>{quantity(lot.scrapped, line.variant.unit)}</Table.Td>
                    </Table.Tr>
                  )),
                )}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Stack>
      )}
    </Stack>
  );
};

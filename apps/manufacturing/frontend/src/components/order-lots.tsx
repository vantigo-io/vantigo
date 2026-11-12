import { Alert, Anchor, Badge, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { type ManufacturingOrder, orderLotsQueryOptions } from "../api/orders";
import { refusalMessage, useManufacturingFormat } from "../lib/format";

/** Inventory's page of a lot. */
const lotHref = (lotId: number) => `/inventory/lots/${lotId}`;

/** A lot's number linked to its page in Inventory; its id when no number was kept. */
const LotLink = ({ lotId, number }: { lotId: number; number: string }) => (
  <Anchor size="sm" href={lotHref(lotId)}>
    {number || `#${lotId}`}
  </Anchor>
);

/**
 * The order's lots (manufacturing phase 2 M83), on the order page's Lots tab
 * and the work order's page: the lots its productions put the output in, and
 * per component line the lots they took, each linked to its page in
 * Inventory.
 */
export const OrderLots = ({ order }: { order: Pick<ManufacturingOrder, "id"> & { variant: { unit: string } } }) => {
  const { t, quantity, day } = useManufacturingFormat();
  const lots = useQuery(orderLotsQueryOptions(order.id));
  if (lots.isPending) return <ContentSkeleton />;
  if (lots.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {refusalMessage(t, lots.error)}
      </Alert>
    );
  const { outputs, components } = lots.data;
  if (outputs.length === 0 && components.length === 0) return <Text c="dimmed">{t("noLots")}</Text>;
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
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {outputs.map((lot) => (
                  <Table.Tr key={lot.lotId}>
                    <Table.Td>
                      <LotLink lotId={lot.lotId} number={lot.lotNumber} />
                      {lot.serial && (
                        <Badge ml="xs" size="xs" variant="light">
                          {t("serialBadge")}
                        </Badge>
                      )}
                    </Table.Td>
                    <Table.Td>{quantity(lot.quantity, order.variant.unit)}</Table.Td>
                    <Table.Td>{lot.productions}</Table.Td>
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
                        <LotLink lotId={lot.lotId} number={lot.lotNumber} />
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

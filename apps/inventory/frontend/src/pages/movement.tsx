import { Alert, Anchor, Badge, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { InventoryLocationRef } from "../api/locations";
import { type InventoryMovement, movementQueryOptions } from "../api/movements";
import { enteredText } from "../lib/breakdown";
import { movementKindColor, useInventoryFormat } from "../lib/format";
import {
  countLinkOptions,
  INVENTORY_HOME_ROUTE_PATH,
  locationLinkOptions,
  lotLinkOptions,
  MOVEMENTS_ROUTE_PATH,
  siteLinkOptions,
  sourceHref,
  transferLinkOptions,
  variantStockLinkOptions,
} from "../lib/routes";

const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <div>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm" component="div">
      {children}
    </Text>
  </div>
);

const LocationCell = ({ location }: { location?: InventoryLocationRef }) =>
  location ? (
    <Anchor size="sm" ff="monospace" renderRoot={(props) => <Link {...locationLinkOptions(location.id)} {...props} />}>
      {location.fullCode}
    </Anchor>
  ) : (
    <Text size="sm" c="dimmed">
      —
    </Text>
  );

/** A movement's source as text, linked to its page in another module when it has one (M5). */
const SourceText = ({ source }: { source: NonNullable<InventoryMovement["source"]> }) => {
  const text = [source.module, source.kind, source.id, source.lineId].filter(Boolean).join(" · ");
  const href = sourceHref(source);
  return href ? (
    <Anchor size="sm" renderRoot={(props) => <Link to={href as never} {...props} />}>
      {text}
    </Anchor>
  ) : (
    text
  );
};

/**
 * One movement (I30): its header — kind, site, when and by whom, the reason
 * or the supplier, the note — the source record when another module caused
 * it, and its lines with the variant as it was snapshotted when posted —
 * and what a line was entered as, in packagings (I25). A count movement
 * links to its count.
 * Movements are never edited; a mistake is put right by a new adjustment.
 */
export const MovementPage = () => {
  const { t, quantity, number, stamp, day } = useInventoryFormat();
  const { movementId: rawId } = useParams({ strict: false }) as { movementId: string };
  const movementId = Number(rawId);
  const movement = useQuery(movementQueryOptions(movementId));
  const data = movement.data;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navMovements"), to: MOVEMENTS_ROUTE_PATH },
          { label: t("movementNumber", { id: movementId }) },
        ]}
        title={
          <Group gap="sm">
            <span>{t("movementNumber", { id: movementId })}</span>
            {data && (
              <Badge variant="light" color={movementKindColor[data.kind]}>
                {t(`kind_${data.kind}`)}
              </Badge>
            )}
          </Group>
        }
      />
      {movement.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {movement.error.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={5} />
      ) : (
        <>
          <PageSection>
            <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
              <Fact label={t("site")}>
                <Anchor size="sm" renderRoot={(props) => <Link {...siteLinkOptions(data.site.id)} {...props} />}>
                  {data.site.code} · {data.site.name}
                </Anchor>
              </Fact>
              <Fact label={t("columnPostedAt")}>{stamp(data.postedAt)}</Fact>
              <Fact label={t("columnPostedBy")}>{data.postedBy.displayName}</Fact>
              {data.reason && <Fact label={t("reason")}>{t(`reason_${data.reason}`)}</Fact>}
              {data.countId !== undefined && (
                <Fact label={t("kind_count")}>
                  <Anchor
                    size="sm"
                    renderRoot={(props) => <Link {...countLinkOptions(data.countId ?? 0)} {...props} />}
                  >
                    {t("countNumber", { id: data.countId })}
                  </Anchor>
                </Fact>
              )}
              {data.supplierName && <Fact label={t("supplierName")}>{data.supplierName}</Fact>}
              {data.supplierReference && <Fact label={t("supplierReference")}>{data.supplierReference}</Fact>}
              {data.transfer && (
                <Fact label={t("transfer")}>
                  <Anchor
                    size="sm"
                    renderRoot={(props) => <Link {...transferLinkOptions(data.transfer?.id ?? 0)} {...props} />}
                  >
                    {t("transferNumber", { number: data.transfer.number })}
                  </Anchor>
                </Fact>
              )}
              {data.source && !data.transfer && (
                <Fact label={t("source")}>
                  <SourceText source={data.source} />
                </Fact>
              )}
            </SimpleGrid>
            {data.note && (
              <Text size="sm" mt="md" style={{ whiteSpace: "pre-wrap" }}>
                {data.note}
              </Text>
            )}
          </PageSection>
          <PageSection>
            <Table.ScrollContainer minWidth={560}>
              <Table striped verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>#</Table.Th>
                    <Table.Th>{t("columnSku")}</Table.Th>
                    <Table.Th>{t("columnProduct")}</Table.Th>
                    <Table.Th>{t("columnFrom")}</Table.Th>
                    <Table.Th>{t("columnTo")}</Table.Th>
                    {data.lines.some((line) => line.lot) && <Table.Th>{t("columnLot")}</Table.Th>}
                    <Table.Th ta="right">{t("columnQuantity")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.lines.map((line) => (
                    <Table.Tr key={line.lineNo}>
                      <Table.Td>{line.lineNo}</Table.Td>
                      <Table.Td>
                        <Anchor
                          size="sm"
                          renderRoot={(props) => <Link {...variantStockLinkOptions(line.variant.id)} {...props} />}
                        >
                          {line.variant.sku}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{line.variant.productName}</Table.Td>
                      <Table.Td>
                        <LocationCell location={line.from} />
                      </Table.Td>
                      <Table.Td>
                        <LocationCell location={line.to} />
                      </Table.Td>
                      {data.lines.some((one) => one.lot) && (
                        <Table.Td>
                          {line.lot ? (
                            <Group gap={6} wrap="nowrap">
                              <Anchor
                                size="sm"
                                ff="monospace"
                                renderRoot={(props) => <Link {...lotLinkOptions(line.lot?.id ?? 0)} {...props} />}
                              >
                                {line.lot.number}
                              </Anchor>
                              {line.lot.expiry && (
                                <Text size="xs" c="dimmed">
                                  {day(line.lot.expiry)}
                                </Text>
                              )}
                              {line.expiredOverride && (
                                <Badge size="xs" variant="light" color="red">
                                  {t("expired")}
                                </Badge>
                              )}
                            </Group>
                          ) : (
                            "—"
                          )}
                        </Table.Td>
                      )}
                      <Table.Td ta="right">
                        {quantity(line.quantity, line.variant.unit)}
                        {line.entered && (
                          <Text size="xs" c="dimmed" aria-label={t("enteredAs")}>
                            {enteredText(line.entered, line.variant.unit, number)}
                          </Text>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </PageSection>
        </>
      )}
    </Stack>
  );
};

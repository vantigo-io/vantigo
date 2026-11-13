import { Alert, Anchor, Badge, Button, Group, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconLockOpen } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import { siteLocationsQueryOptions } from "../api/locations";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { releaseUnbuild, unbuildQueryOptions } from "../api/orders";
import { NotFoundError } from "../api/request";
import { canManageQuality } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { MANUFACTURING_HOME_ROUTE_PATH, orderLinkOptions, UNBUILDS_ROUTE_PATH } from "../lib/routes";

const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={2}>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    <Text size="sm" component="div">
      {children}
    </Text>
  </Stack>
);

/**
 * One unbuild (manufacturing phase 2 M123), UB-n: what it took apart — from
 * a done order and its lot, or by the bill of materials — where from and to,
 * and what it restored into which lots. An unbuild of a held lot quarantined
 * what it restored; a holder of manufacturing:quality-manage releases that
 * quarantine here once the lots are inspected. Inventory's movement page and
 * a lot's trace link here.
 */
export const UnbuildPage = () => {
  const { t, quantity, stamp } = useManufacturingFormat();
  const { unbuildId } = useParams({ strict: false }) as { unbuildId: string };
  const id = Number(unbuildId);
  const meta = useQuery(manufacturingMetaQueryOptions());
  const query = useQuery(unbuildQueryOptions(id));
  const u = query.data;
  const locations = useQuery({ ...siteLocationsQueryOptions(u?.siteId ?? 0), enabled: u !== undefined });
  const invalidate = useInvalidateManufacturing();
  const release = useMutation({
    mutationFn: () => releaseUnbuild(id),
    onSuccess: async (released) => {
      notifications.show({ color: "teal", title: t("unbuildQuarantineReleased"), message: released.number });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) }),
  });
  const breadcrumbs = [
    { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
    { label: t("navUnbuilds"), to: UNBUILDS_ROUTE_PATH },
    { label: u?.number ?? t("unbuildNumber") },
  ];
  if (query.error)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("unbuildNumber")} />
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {query.error instanceof NotFoundError ? t("unbuildNotFound") : query.error.message}
        </Alert>
      </Stack>
    );
  if (!u)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("unbuildNumber")} />
        <ContentSkeleton rows={3} />
      </Stack>
    );
  const code = (locationId: number) =>
    locations.data?.find((location) => location.id === locationId)?.fullCode ?? String(locationId);
  const held = u.quarantined && u.releasedAt === null;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs}
        title={
          <Group gap="sm">
            {`${u.number} · ${u.variant.sku}`}
            {held && (
              <Badge size="lg" variant="light" color="orange">
                {t("unbuildQuarantined")}
              </Badge>
            )}
          </Group>
        }
        description={u.variant.productName}
        actions={
          held &&
          canManageQuality(meta.data) && (
            <Button
              variant="default"
              data-assist="manufacturing.unbuild.release"
              leftSection={<IconLockOpen size={16} />}
              loading={release.isPending}
              onClick={() => release.mutate()}
            >
              {t("unbuildRelease")}
            </Button>
          )
        }
      />
      {held && (
        <Alert color="orange" icon={<IconAlertCircle size={16} />} title={t("unbuildQuarantined")}>
          {t("unbuildQuarantinedBody")}
        </Alert>
      )}
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
          <Fact label={t("order")}>
            {u.orderId !== null && u.orderNumber ? (
              <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(u.orderId ?? 0)} {...props} />}>
                {u.orderNumber}
              </Anchor>
            ) : (
              t("unbuildFreeStanding")
            )}
          </Fact>
          <Fact label={t("quantity")}>
            {quantity(u.quantity, u.variant.unit)}
            {u.lotNumber && (
              <Text size="xs" c="dimmed">
                {t("lotValue", { number: u.lotNumber })}
              </Text>
            )}
          </Fact>
          <Fact label={t("unbuildFrom")}>{code(u.fromLocationId)}</Fact>
          <Fact label={t("unbuildTo")}>{code(u.toLocationId)}</Fact>
          <Fact label={t("postedAt")}>
            {stamp(u.postedAt)}
            <Text size="xs" c="dimmed">
              {u.postedByDisplay}
            </Text>
          </Fact>
          {u.releasedAt && (
            <Fact label={t("unbuildReleasedAt")}>
              {stamp(u.releasedAt)}
              <Text size="xs" c="dimmed">
                {u.releasedByDisplay}
              </Text>
            </Fact>
          )}
          {u.note && <Fact label={t("note")}>{u.note}</Fact>}
        </SimpleGrid>
      </PageSection>
      <PageSection title={t("restored")}>
        <Table.ScrollContainer minWidth={500}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("product")}</Table.Th>
                <Table.Th>{t("quantity")}</Table.Th>
                <Table.Th>{t("unbuildLot")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {u.lines.map((line) => (
                <Table.Tr key={`${line.variant.id}-${line.lotId ?? 0}`}>
                  <Table.Td>
                    <Text size="sm">{line.variant.sku}</Text>
                    <Text size="xs" c="dimmed">
                      {line.variant.productName}
                    </Text>
                  </Table.Td>
                  <Table.Td>{quantity(line.quantity, line.variant.unit)}</Table.Td>
                  <Table.Td>{line.lotNumber ?? "—"}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </PageSection>
    </Stack>
  );
};

import { Alert, Anchor, Badge, Button, Group, Select, SimpleGrid, Stack, Table, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconLockOpen } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import {
  closeDeviation,
  deviationDispositions,
  deviationQueryOptions,
  type ManufacturingDeviation,
  type ManufacturingDeviationDisposition,
  releaseDeviationHolds,
  useDeviationsLive,
} from "../api/quality";
import { isRevisionConflict, NotFoundError } from "../api/request";
import { Markdown } from "../components/markdown";
import { canManageQuality } from "../lib/capabilities";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import { deviationStatusColor } from "../lib/quality";
import {
  DEVIATIONS_ROUTE_PATH,
  MANUFACTURING_HOME_ROUTE_PATH,
  orderLinkOptions,
  workOrderLinkOptions,
} from "../lib/routes";

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

/** The close form (M99): a disposition and a note, under the deviation's revision. */
const CloseForm = ({ deviation }: { deviation: ManufacturingDeviation }) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [disposition, setDisposition] = useState<ManufacturingDeviationDisposition | null>(null);
  const [note, setNote] = useState("");
  const close = useMutation({
    mutationFn: () =>
      closeDeviation(deviation.id, {
        disposition: disposition ?? "use_as_is",
        closingNote: note.trim(),
        revision: deviation.revision,
      }),
    onSuccess: async (closed) => {
      notifications.show({ color: "teal", title: t("deviationClosedTitle"), message: closed.number });
      await invalidate();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) await invalidate();
    },
  });
  return (
    <Stack gap="sm" data-assist="manufacturing.deviation.close">
      <Select
        label={t("disposition")}
        withAsterisk
        w={260}
        data={deviationDispositions.map((value) => ({ value, label: t(`disposition_${value}`) }))}
        value={disposition}
        onChange={(next) => setDisposition(next as ManufacturingDeviationDisposition | null)}
      />
      {disposition && (
        <Text size="sm" c="dimmed">
          {t(`dispositionHint_${disposition}`)}
        </Text>
      )}
      <Textarea
        label={t("closingNote")}
        withAsterisk
        autosize
        minRows={2}
        maxLength={2000}
        value={note}
        onChange={(event) => setNote(event.currentTarget.value)}
      />
      {close.error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {refusalMessage(t, close.error)}
        </Alert>
      )}
      <Group>
        <Button loading={close.isPending} disabled={!disposition || note.trim() === ""} onClick={() => close.mutate()}>
          {t("closeDeviation")}
        </Button>
      </Group>
    </Stack>
  );
};

/**
 * One deviation (manufacturing phase 2 M99): what it is about and where it
 * was opened from, the lots it holds in quarantine — each linked to its page
 * in Inventory — and, for a holder of manufacturing:quality-manage, Close
 * with a disposition and a note, and, once closed as scrap or return,
 * Release its holds.
 */
export const DeviationPage = () => {
  const { t, stamp } = useManufacturingFormat();
  const { deviationId } = useParams({ strict: false }) as { deviationId: string };
  const id = Number(deviationId);
  useDeviationsLive();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const query = useQuery(deviationQueryOptions(id));
  const invalidate = useInvalidateManufacturing();
  const release = useMutation({
    mutationFn: (deviation: ManufacturingDeviation) => releaseDeviationHolds(deviation.id, deviation.revision),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("holdsReleased"), message: "" });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDoThat"), message: refusalMessage(t, error as Error) }),
  });
  const d = query.data;
  const breadcrumbs = [
    { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
    { label: t("deviations"), to: DEVIATIONS_ROUTE_PATH },
    { label: d?.number ?? t("deviation") },
  ];
  if (query.error)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("deviation")} />
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {query.error instanceof NotFoundError ? t("deviationNotFound") : query.error.message}
        </Alert>
      </Stack>
    );
  if (!d)
    return (
      <Stack gap="lg">
        <PageHeader breadcrumbs={breadcrumbs} title={t("deviation")} />
        <ContentSkeleton rows={3} />
      </Stack>
    );
  const manage = canManageQuality(meta.data);
  const releasable = d.status === "closed" && !d.holdsReleased && d.lots.length > 0;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs}
        title={
          <Group gap="sm">
            {`${d.number} · ${d.title}`}
            <Badge size="lg" variant="light" color={deviationStatusColor[d.status]}>
              {t(`deviationStatus_${d.status}`)}
            </Badge>
            {d.quarantine && (
              <Badge size="lg" variant="light" color="orange">
                {t("quarantine")}
              </Badge>
            )}
          </Group>
        }
        actions={
          manage &&
          releasable && (
            <Button
              variant="default"
              leftSection={<IconLockOpen size={16} />}
              loading={release.isPending}
              onClick={() => release.mutate(d)}
            >
              {t("releaseHolds")}
            </Button>
          )
        }
      />
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
          <Fact label={t("order")}>
            {d.order ? (
              <Anchor size="sm" renderRoot={(props) => <Link {...orderLinkOptions(d.order?.id ?? 0)} {...props} />}>
                {d.order.number}
              </Anchor>
            ) : (
              "—"
            )}
          </Fact>
          <Fact label={t("workOrder")}>
            {d.workOrderId !== null ? (
              <Anchor
                size="sm"
                renderRoot={(props) => <Link {...workOrderLinkOptions(d.workOrderId ?? 0)} {...props} />}
              >
                {d.workOrderName}
              </Anchor>
            ) : (
              "—"
            )}
          </Fact>
          <Fact label={t("variant")}>{d.variant ? `${d.variant.sku} · ${d.variant.productName}` : "—"}</Fact>
          <Fact label={t("deviationOpenedAt")}>{`${stamp(d.openedAt)}, ${d.openedBy.displayName}`}</Fact>
          {d.closedAt && (
            <Fact label={t("deviationClosedAt")}>{`${stamp(d.closedAt)}, ${d.closedBy?.displayName ?? ""}`}</Fact>
          )}
          {d.disposition && <Fact label={t("disposition")}>{t(`disposition_${d.disposition}`)}</Fact>}
        </SimpleGrid>
      </PageSection>
      <PageSection title={t("deviationDescription")}>
        {d.description.trim() ? (
          <Markdown>{d.description}</Markdown>
        ) : (
          <Text size="sm" c="dimmed">
            {t("noDescription")}
          </Text>
        )}
        {d.closingNote && (
          <Stack gap={2} mt="md">
            <Text size="sm" c="dimmed">
              {t("closingNote")}
            </Text>
            <Text size="sm">{d.closingNote}</Text>
          </Stack>
        )}
      </PageSection>
      <PageSection title={t("heldLots")}>
        {d.lots.length === 0 ? (
          <Text size="sm" c="dimmed">
            {d.quarantine && d.status === "open" ? t("heldLotsLater") : t("noHeldLots")}
          </Text>
        ) : (
          <Table data-assist="manufacturing.deviation.lots">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("lotNumber")}</Table.Th>
                <Table.Th>{t("heldSince")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {d.lots.map((lot) => (
                <Table.Tr key={lot.lotId}>
                  <Table.Td>
                    {meta.data?.canTraceLots ? (
                      <Anchor
                        size="sm"
                        renderRoot={(props) => <Link to={`/inventory/lots/${lot.lotId}` as never} {...props} />}
                      >
                        {lot.lotNumber}
                      </Anchor>
                    ) : (
                      lot.lotNumber
                    )}
                  </Table.Td>
                  <Table.Td>{stamp(lot.heldAt)}</Table.Td>
                  <Table.Td>{d.holdsReleased ? t("holdReleased") : t("holdActive")}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {d.status === "closed" && !d.holdsReleased && d.lots.length > 0 && (
          <Alert mt="md" color="yellow" icon={<IconAlertCircle size={16} />} title={t("holdsKeptTitle")}>
            {t("holdsKeptHint")}
          </Alert>
        )}
      </PageSection>
      {d.status === "open" && manage && (
        <PageSection title={t("closeDeviation")}>
          <CloseForm deviation={d} />
        </PageSection>
      )}
    </Stack>
  );
};

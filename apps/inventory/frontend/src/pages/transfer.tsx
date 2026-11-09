import { Alert, Anchor, Badge, Button, Group, SimpleGrid, Stack, Table, Text, Title } from "@mantine/core";
import {
  IconAlertCircle,
  IconArrowRight,
  IconPencil,
  IconPrinter,
  IconTrash,
  IconTruckDelivery,
  IconX,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { inventoryMetaQueryOptions } from "../api/meta";
import { sitesQueryOptions } from "../api/sites";
import { type InventoryTransfer, type InventoryTransferAction, transferQueryOptions } from "../api/transfers";
import { SendTransferDialog } from "../components/send-transfer-dialog";
import { TransferForm } from "../components/transfer-form";
import { TransferStatusBadge } from "../components/transfer-status";
import { enteredText } from "../lib/breakdown";
import { canTransfer, canViewTransfers } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import {
  INVENTORY_HOME_ROUTE_PATH,
  movementLinkOptions,
  receiveTransferLinkOptions,
  siteLinkOptions,
  TRANSFERS_ROUTE_PATH,
  variantStockLinkOptions,
} from "../lib/routes";
import { isCancellable, isReceivable, useTransferActions } from "../lib/transfer-actions";

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

/** A quantity cell that says nothing for nothing, as the transfer's columns do. */
const Amount = ({ value, unit }: { value: number; unit: string }) => {
  const { quantity } = useInventoryFormat();
  return value === 0 ? <Text c="dimmed">—</Text> : quantity(value, unit);
};

/** One action of the history (I38): who, when, what it did and where each part went. */
const ActionEntry = ({ action, transfer }: { action: InventoryTransferAction; transfer: InventoryTransfer }) => {
  const { t, quantity, stamp } = useInventoryFormat();
  const lineOf = (lineId: number) => transfer.lines.find((line) => line.id === lineId);
  const values = { who: action.postedBy.displayName };
  const summary =
    action.kind === "send"
      ? t("actionSent", { ...values, site: transfer.fromSite.code })
      : action.kind === "cancel"
        ? t("actionCancelled", { ...values, site: transfer.fromSite.code })
        : action.lines.some((line) => line.kind === "received")
          ? t("actionReceived", { ...values, site: transfer.toSite.code })
          : t("actionClosed", values);
  return (
    <Stack gap={4} data-action={action.kind}>
      <Group gap="sm" wrap="nowrap" justify="space-between">
        <Group gap="sm">
          <Text size="sm" c="dimmed">
            {stamp(action.postedAt)}
          </Text>
          <Text size="sm" fw={500}>
            {summary}
          </Text>
        </Group>
        {action.movementId !== undefined && (
          <Anchor
            size="sm"
            renderRoot={(props) => <Link {...movementLinkOptions(action.movementId ?? 0)} {...props} />}
          >
            {t("movementNumber", { id: action.movementId })}
          </Anchor>
        )}
      </Group>
      {action.closeReason && (
        <Text size="sm">
          {t(`closeReason.${action.closeReason}`)}
          {action.closeNote ? ` — ${action.closeNote}` : ""}
        </Text>
      )}
      <Stack gap={0} pl="md">
        {action.lines.map((line) => {
          const of = lineOf(line.lineId);
          return (
            <Text key={`${line.lineId}-${line.kind}`} size="xs" c="dimmed">
              {t(`actionLine_${line.kind}`, {
                quantity: quantity(line.quantity, of?.variant.unit),
                sku: of?.variant.sku ?? "",
              })}
              {line.location ? ` · ${line.location.fullCode}` : ""}
            </Text>
          );
        })}
      </Stack>
    </Stack>
  );
};

/**
 * One transfer (phase 2 design §7): its sites, status, reference, expected
 * arrival and note; its lines with what was sent, received, recorded missing
 * and is still in transit (I46); and its history — every send, receive and
 * cancel with who, when and where each part went (I38). The buttons follow
 * the status: a draft is edited, sent, sent and received, or deleted; a sent
 * transfer is received, or cancelled while nothing of it has arrived; one
 * sent or later prints its følgeseddel. Writes need `inventory:transfers-manage` (I63).
 */
export const TransferPage = () => {
  const { t, number, day, stamp } = useInventoryFormat();
  const { transferId: rawId } = useParams({ strict: false }) as { transferId: string };
  const transferId = Number(rawId);
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const viewable = canViewTransfers(meta.data);
  const query = useQuery({ ...transferQueryOptions(transferId), enabled: viewable });
  const writes = canTransfer(meta.data);
  const [editing, setEditing] = useState(false);
  const [sending, setSending] = useState<"send" | "sendAndReceive">();
  const sites = useQuery({ ...sitesQueryOptions(), enabled: editing });
  const actions = useTransferActions();
  // Made once per visit, so a retried cancel replays rather than cancelling twice (D I14).
  const [cancelKey] = useState(() => crypto.randomUUID());
  const data = query.data;
  const title = data ? t("transferNumber", { number: data.number }) : t("transferById", { id: transferId });
  const toList = () => void navigate({ to: TRANSFERS_ROUTE_PATH });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navTransfers"), to: TRANSFERS_ROUTE_PATH },
          { label: title },
        ]}
        title={
          <Group gap="sm">
            <span>{title}</span>
            {data && <TransferStatusBadge status={data.status} />}
            {data && isReceivable(data) && data.inTransit > 0 && (
              <Badge variant="light" color="blue">
                {t("inTransitQuantity", { quantity: number(data.inTransit) })}
              </Badge>
            )}
          </Group>
        }
        description={
          data && (
            <Group gap={6}>
              <Anchor size="sm" renderRoot={(props) => <Link {...siteLinkOptions(data.fromSite.id)} {...props} />}>
                {data.fromSite.code} · {data.fromSite.name}
              </Anchor>
              <IconArrowRight size={14} />
              <Anchor size="sm" renderRoot={(props) => <Link {...siteLinkOptions(data.toSite.id)} {...props} />}>
                {data.toSite.code} · {data.toSite.name}
              </Anchor>
            </Group>
          )
        }
        actions={
          data &&
          !editing && (
            <Group gap="xs">
              {data.status !== "draft" && (
                <Button
                  variant="default"
                  leftSection={<IconPrinter size={16} />}
                  onClick={() => void actions.printNote(data)}
                >
                  {t("printNote")}
                </Button>
              )}
              {writes && data.status === "draft" && (
                <>
                  <Button variant="default" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
                    {t("edit")}
                  </Button>
                  <Button leftSection={<IconTruckDelivery size={16} />} onClick={() => setSending("send")}>
                    {t("sendEllipsis")}
                  </Button>
                  <Button variant="light" onClick={() => setSending("sendAndReceive")}>
                    {t("sendAndReceiveEllipsis")}
                  </Button>
                  <Button
                    variant="subtle"
                    color="red"
                    leftSection={<IconTrash size={16} />}
                    onClick={() => actions.deleteDraft(data, toList)}
                  >
                    {t("deleteDraft")}
                  </Button>
                </>
              )}
              {writes && isReceivable(data) && (
                <Button renderRoot={(props) => <Link {...receiveTransferLinkOptions(data.id)} {...props} />}>
                  {t("receiveEllipsis")}
                </Button>
              )}
              {writes && isCancellable(data) && (
                <Button
                  variant="subtle"
                  color="red"
                  leftSection={<IconX size={16} />}
                  onClick={() => actions.cancel(data, cancelKey)}
                >
                  {t("cancelTransfer")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {meta.data && !viewable ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_viewTransfers")}
        </Alert>
      ) : (meta.error ?? query.error) ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {(meta.error ?? query.error)?.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={5} />
      ) : editing ? (
        sites.data ? (
          <PageSection>
            <TransferForm
              sites={sites.data}
              transfer={data}
              onFinished={() => setEditing(false)}
              onDeleted={toList}
              onCancel={() => setEditing(false)}
            />
          </PageSection>
        ) : (
          <ContentSkeleton rows={5} />
        )
      ) : (
        <>
          <PageSection>
            <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
              <Fact label={t("transferReference")}>{data.reference ?? "—"}</Fact>
              <Fact label={t("expectedArrival")}>{data.expectedArrival ? day(data.expectedArrival) : "—"}</Fact>
              <Fact label={t("createdBy")}>
                {data.createdBy.displayName} · {stamp(data.createdAt)}
              </Fact>
              {data.sentAt && (
                <Fact label={t("sentBy")}>
                  {data.sentBy?.displayName ?? "—"} · {stamp(data.sentAt)}
                </Fact>
              )}
              {data.closeReason && (
                <Fact label={t("closeReason")}>
                  {t(`closeReason.${data.closeReason}`)}
                  {data.closeNote ? ` — ${data.closeNote}` : ""}
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
            <Table.ScrollContainer minWidth={720}>
              <Table striped verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("columnSku")}</Table.Th>
                    <Table.Th>{t("columnProduct")}</Table.Th>
                    <Table.Th>{t("columnFrom")}</Table.Th>
                    <Table.Th ta="right">{data.status === "draft" ? t("columnQuantity") : t("columnSent")}</Table.Th>
                    <Table.Th ta="right">{t("columnReceived")}</Table.Th>
                    <Table.Th ta="right">{t("notReceived")}</Table.Th>
                    <Table.Th ta="right">{t("inTransit")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {data.lines.map((line) => (
                    <Table.Tr key={line.id}>
                      <Table.Td>
                        <Anchor
                          size="sm"
                          renderRoot={(props) => <Link {...variantStockLinkOptions(line.variant.id)} {...props} />}
                        >
                          {line.variant.sku}
                        </Anchor>
                        {line.variant.deleted && (
                          <Text size="xs" c="red">
                            {t("variantDeleted")}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>{line.variant.productName}</Table.Td>
                      <Table.Td>
                        <Text size="sm" ff="monospace">
                          {line.fromLocation?.fullCode ?? "—"}
                        </Text>
                      </Table.Td>
                      <Table.Td ta="right">
                        <Amount value={data.status === "draft" ? line.quantity : line.sent} unit={line.variant.unit} />
                        {line.entered && (
                          <Text size="xs" c="dimmed" aria-label={t("enteredAs")}>
                            {enteredText(line.entered, line.variant.unit, number)}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td ta="right">
                        <Amount value={line.received} unit={line.variant.unit} />
                      </Table.Td>
                      <Table.Td ta="right">
                        <Amount value={line.missing} unit={line.variant.unit} />
                      </Table.Td>
                      <Table.Td ta="right">
                        <Amount value={line.inTransit} unit={line.variant.unit} />
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </PageSection>
          {data.actions.length > 0 && (
            <PageSection>
              <Stack gap="sm">
                <Title order={4}>{t("transferHistory")}</Title>
                {data.actions.map((action) => (
                  <ActionEntry key={action.id} action={action} transfer={data} />
                ))}
              </Stack>
            </PageSection>
          )}
        </>
      )}
      {sending && data && (
        <SendTransferDialog
          opened
          transfer={data}
          withReceive={sending === "sendAndReceive"}
          onClose={() => setSending(undefined)}
        />
      )}
    </Stack>
  );
};

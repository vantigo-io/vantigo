import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  FileButton,
  Group,
  List,
  Modal,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconDownload,
  IconLock,
  IconPencil,
  IconPrinter,
  IconRoute,
  IconShieldPause,
  IconTrash,
  IconUpload,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  deleteLotAttachment,
  holdCauses,
  holdKinds,
  type InventoryLot,
  type InventoryLotHold,
  type InventoryLotHoldCause,
  type InventoryLotHoldKind,
  LOT_FILE_TYPES,
  lotAttachmentHref,
  lotQueryOptions,
  MAX_LOT_FILE_BYTES,
  postLotHold,
  putLot,
  releaseLotHold,
  uploadLotAttachment,
  useLotsLive,
} from "../api/lots";
import { inventoryMetaQueryOptions } from "../api/meta";
import { INVENTORY_QUERY_KEY } from "../api/request";
import {
  blockCauses,
  blockLot,
  downstreamInStock,
  type InventoryLotBlock,
  type InventoryLotBlockCause,
  tracedDeliveries,
  traceQueryOptions,
} from "../api/trace";
import { LotStatusBadges } from "../components/lot-status";
import { PrintLotLabelsDialog } from "../components/print-lot-labels-dialog";
import { canManageLots, canViewLots } from "../lib/capabilities";
import { movementKindColor, refusalMessage, useInventoryFormat } from "../lib/format";
import {
  countLinkOptions,
  deviationHref,
  INVENTORY_HOME_ROUTE_PATH,
  LOTS_ROUTE_PATH,
  locationLinkOptions,
  lotLinkOptions,
  movementLinkOptions,
  reportDeviationHref,
  siteLinkOptions,
  traceLinkOptions,
  unbuildHref,
  variantStockLinkOptions,
} from "../lib/routes";

const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm" component="div">
      {children}
    </Text>
  </Stack>
);

/** A lot page's movement lines are the latest this many (M61): the server's own bound. */
const LOT_PAGE_MOVEMENTS = 200;

/** Another module's name as the host labels it; the key itself when the host gives none. */
type ModuleLabel = (module: string) => string;
const ownKey: ModuleLabel = (module) => module;

/** Whether the lot page itself has an active hold of the kind on the lot: it holds a lot once per kind (409 hold_exists). */
const pageHolds = (lot: InventoryLot, kind: InventoryLotHoldKind) =>
  lot.holds.some(
    (hold) =>
      !hold.releasedAt &&
      hold.kind === kind &&
      hold.source.module === "inventory" &&
      hold.source.kind === "lot" &&
      hold.source.id === String(lot.id),
  );

/** The document that made a lot or put a hold: this page, a movement linked, another module's named. */
const SourceLink = ({
  source,
  lotId,
  moduleLabel,
}: {
  source: InventoryLotHold["source"];
  lotId: number;
  moduleLabel: ModuleLabel;
}) => {
  const { t } = useInventoryFormat();
  if (source.module === "inventory" && source.kind === "lot" && source.id === String(lotId))
    return <>{t("holdSource_lot")}</>;
  // A receipt's inspection hold names its movement (manufacturing phase 2 M100); a deviation's, the deviation (M99).
  if (source.module === "inventory" && source.kind === "receipt" && /^\d+$/.test(source.id))
    return (
      <Anchor size="sm" renderRoot={(props) => <Link {...movementLinkOptions(Number(source.id))} {...props} />}>
        {t("receiptById", { id: source.id })}
      </Anchor>
    );
  const deviation = deviationHref(source);
  if (deviation)
    return (
      <Anchor size="sm" renderRoot={(props) => <Link to={deviation as never} {...props} />}>
        {t("deviationById", { id: source.id })}
      </Anchor>
    );
  // An unbuild of a held lot quarantines what it restored; its page releases it (M123).
  const unbuild = unbuildHref(source);
  if (unbuild)
    return (
      <Anchor size="sm" renderRoot={(props) => <Link to={unbuild as never} {...props} />}>
        {t("holdSource_unbuild")}
      </Anchor>
    );
  if (source.module === "inventory" && source.kind === "movement" && /^\d+$/.test(source.id))
    return (
      <Anchor size="sm" renderRoot={(props) => <Link {...movementLinkOptions(Number(source.id))} {...props} />}>
        {t("movementById", { id: source.id })}
      </Anchor>
    );
  // A lot the opening-balance import made names the import's movement; one a count line made, the count (M69, M71).
  if (source.module === "inventory" && source.kind === "import" && /^\d+$/.test(source.id))
    return (
      <Anchor size="sm" renderRoot={(props) => <Link {...movementLinkOptions(Number(source.id))} {...props} />}>
        {t("originImport", { id: source.id })}
      </Anchor>
    );
  if (source.module === "inventory" && source.kind === "count" && /^\d+$/.test(source.id))
    return (
      <Anchor size="sm" renderRoot={(props) => <Link {...countLinkOptions(Number(source.id))} {...props} />}>
        {t("countNumber", { id: source.id })}
      </Anchor>
    );
  return <>{`${moduleLabel(source.module)} · ${source.kind} ${source.id}`}</>;
};

/** The edit dialog (M61): the supplier lot, the three dates and the note, replaced whole at the revision read. */
const EditLotDialog = ({ lot, onClose }: { lot: InventoryLot; onClose: () => void }) => {
  const { t } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [supplierLot, setSupplierLot] = useState(lot.supplierLot ?? "");
  const [productionDate, setProductionDate] = useState<string | null>(lot.productionDate ?? null);
  const [bestBefore, setBestBefore] = useState<string | null>(lot.bestBefore ?? null);
  const [expiry, setExpiry] = useState<string | null>(lot.expiry ?? null);
  const [note, setNote] = useState(lot.note ?? "");
  const saving = useMutation({
    mutationFn: () =>
      putLot(
        lot.id,
        {
          ...(supplierLot.trim() ? { supplierLot: supplierLot.trim() } : {}),
          ...(productionDate ? { productionDate } : {}),
          ...(bestBefore ? { bestBefore } : {}),
          ...(expiry ? { expiry } : {}),
          ...(note.trim() ? { note: note.trim() } : {}),
        },
        lot.revision,
      ),
    onSuccess: (saved) => {
      queryClient.setQueryData(lotQueryOptions(lot.id).queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY] });
      notifications.show({ color: "teal", message: t("lotSaved") });
      onClose();
    },
  });
  const date = (label: string, value: string | null, set: (next: string | null) => void) => (
    <DatePickerInput label={label} clearable valueFormat={t("dateInputFormat")} value={value} onChange={set} />
  );
  return (
    <Modal opened onClose={onClose} title={t("editLotTitle")}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t("editLotHint")}
        </Text>
        <TextInput
          label={t("supplierLot")}
          maxLength={40}
          value={supplierLot}
          onChange={(event) => setSupplierLot(event.currentTarget.value)}
        />
        {date(t("productionDate"), productionDate, setProductionDate)}
        {date(t("bestBefore"), bestBefore, setBestBefore)}
        {date(t("expiryDate"), expiry, setExpiry)}
        <Textarea
          label={t("note")}
          maxLength={2000}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        {saving.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, saving.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={saving.isPending} onClick={() => saving.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Putting a hold of the lot page (M64): its kind, a cause and a note. */
const HoldDialog = ({ lot, kind, onClose }: { lot: InventoryLot; kind: InventoryLotHoldKind; onClose: () => void }) => {
  const { t } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [cause, setCause] = useState<InventoryLotHoldCause>(kind === "block" ? "recall" : "inspection");
  const [note, setNote] = useState("");
  const holding = useMutation({
    mutationFn: () => postLotHold(lot.id, { kind, cause, ...(note.trim() ? { note: note.trim() } : {}) }),
    onSuccess: (saved) => {
      queryClient.setQueryData(lotQueryOptions(lot.id).queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY] });
      notifications.show({ color: "teal", message: t("lotHeld") });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={t(`holdTitle_${kind}`)}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t("holdHint")}
        </Text>
        <Select
          label={t("holdCause")}
          allowDeselect={false}
          data={holdCauses.map((value) => ({ value, label: t(`holdCause_${value}`) }))}
          value={cause}
          onChange={(next) => next && setCause(next as InventoryLotHoldCause)}
        />
        <Textarea
          label={t("holdNote")}
          maxLength={1000}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        {holding.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, holding.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button
            color={kind === "block" ? "red" : "orange"}
            loading={holding.isPending}
            onClick={() => holding.mutate()}
          >
            {t("putHold")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * Blocking the lot with its downstream list (manufacturing phase 2 M92): the
 * usage trace's summary first — the lots made from it still in stock, and the
 * deliveries that received it — then a block of the lot page on the lot and,
 * by default, on every lot made from it. The server walks the trace again
 * inside the block, so the answer may name lots the summary did not show,
 * which are marked new.
 */
const BlockDialog = ({ lot, onClose }: { lot: InventoryLot; onClose: () => void }) => {
  const { t, quantity } = useInventoryFormat();
  const queryClient = useQueryClient();
  const summary = useQuery(traceQueryOptions({ lotId: lot.id, direction: "usage" }));
  const [cause, setCause] = useState<InventoryLotBlockCause>("recall");
  const [note, setNote] = useState("");
  const [downstream, setDownstream] = useState(true);
  const [done, setDone] = useState<InventoryLotBlock>();
  const made = summary.data ? downstreamInStock(summary.data) : [];
  const delivered = summary.data ? tracedDeliveries(summary.data) : [];
  const blocking = useMutation({
    mutationFn: () => blockLot(lot.id, { cause, downstream, ...(note.trim() ? { note: note.trim() } : {}) }),
    onSuccess: (saved) => {
      queryClient.setQueryData(lotQueryOptions(lot.id).queryKey, saved.lot);
      void queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY] });
      setDone(saved);
    },
  });
  if (done) {
    const shown = new Set(made.map((node) => node.lot?.id));
    return (
      <Modal opened onClose={onClose} title={t("holdTitle_block")}>
        <Stack gap="sm">
          <Text size="sm">{t("blockedCount", { count: done.blocked.length })}</Text>
          <List size="sm" spacing={4}>
            {done.blocked.map((row) => (
              <List.Item key={row.id}>
                <Group gap={6}>
                  <Anchor
                    size="sm"
                    ff="monospace"
                    renderRoot={(props) => <Link {...lotLinkOptions(row.id)} {...props} />}
                  >
                    {row.number}
                  </Anchor>
                  <Text size="sm" c="dimmed">
                    {row.sku} · {row.productName}
                  </Text>
                  {!row.root && !shown.has(row.id) && (
                    <Badge size="xs" color="red" variant="light">
                      {t("blockedNew")}
                    </Badge>
                  )}
                </Group>
              </List.Item>
            ))}
          </List>
          <Group justify="flex-end">
            <Button onClick={onClose}>{t("done")}</Button>
          </Group>
        </Stack>
      </Modal>
    );
  }
  return (
    <Modal opened onClose={onClose} title={t("holdTitle_block")} size="lg">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t("blockHint")}
        </Text>
        {summary.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, summary.error)}
          </Alert>
        ) : !summary.data ? (
          <ContentSkeleton rows={2} />
        ) : (
          <Stack gap={6} data-testid="block-summary">
            <Text size="sm" fw={600}>
              {t("blockMadeFromIt")}
            </Text>
            {made.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("blockMadeNone")}
              </Text>
            ) : (
              <List size="sm" spacing={2}>
                {made.map((node) => (
                  <List.Item key={node.key}>
                    {node.lot?.number} · {node.variant.sku} · {quantity(node.lot?.onHand ?? 0, node.variant.unit)}
                  </List.Item>
                ))}
              </List>
            )}
            <Text size="sm" fw={600}>
              {t("blockDeliveredTo")}
            </Text>
            {delivered.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("blockDeliveredNone")}
              </Text>
            ) : (
              <List size="sm" spacing={2}>
                {delivered.map((node) => (
                  <List.Item key={node.key}>
                    {node.delivery
                      ? `${t("traceDelivery", { delivery: node.delivery.deliveryNumber, order: node.delivery.orderNumber })} · ${
                          node.delivery.customerName ?? t("traceCustomerId", { id: node.delivery.customerId })
                        }`
                      : t("movementNumber", { id: node.movement?.id })}
                    {` · ${quantity(node.quantity ?? 0, node.variant.unit)} ${node.variant.sku}`}
                  </List.Item>
                ))}
              </List>
            )}
          </Stack>
        )}
        <Select
          label={t("holdCause")}
          allowDeselect={false}
          data={blockCauses.map((value) => ({ value, label: t(`holdCause_${value}`) }))}
          value={cause}
          onChange={(next) => next && setCause(next as InventoryLotBlockCause)}
        />
        <Textarea
          label={t("holdNote")}
          maxLength={1000}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        <Checkbox
          label={t("blockDownstream")}
          checked={downstream}
          onChange={(event) => setDownstream(event.currentTarget.checked)}
        />
        {blocking.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, blocking.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button color="red" loading={blocking.isPending} onClick={() => blocking.mutate()}>
            {t("blockLot")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** Whether a hold is this lot page's own block, whose release may reach the lots it blocked (M92). */
const pageBlock = (lot: InventoryLot, hold: InventoryLotHold) =>
  hold.kind === "block" &&
  hold.source.module === "inventory" &&
  hold.source.kind === "lot" &&
  hold.source.id === String(lot.id);

/** Releasing one of Inventory's own holds (M64), with why. */
const ReleaseDialog = ({ lot, hold, onClose }: { lot: InventoryLot; hold: InventoryLotHold; onClose: () => void }) => {
  const { t } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [note, setNote] = useState("");
  const [downstream, setDownstream] = useState(true);
  const reaches = pageBlock(lot, hold);
  const releasing = useMutation({
    mutationFn: () => releaseLotHold(lot.id, hold.id, note, reaches && downstream),
    onSuccess: (saved) => {
      queryClient.setQueryData(lotQueryOptions(lot.id).queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY] });
      notifications.show({ color: "teal", message: t("holdReleased") });
      onClose();
    },
  });
  return (
    <Modal opened onClose={onClose} title={t("releaseTitle")}>
      <Stack gap="sm">
        <Textarea
          label={t("releaseNote")}
          maxLength={1000}
          minRows={2}
          value={note}
          onChange={(event) => setNote(event.currentTarget.value)}
        />
        {reaches && (
          <Checkbox
            label={t("releaseDownstream")}
            checked={downstream}
            onChange={(event) => setDownstream(event.currentTarget.checked)}
          />
        )}
        {releasing.error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refusalMessage(t, releasing.error)}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={releasing.isPending} onClick={() => releasing.mutate()}>
            {t("releaseHold")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/** The holds panel (M61, M64): every hold, active ones first, with Quarantine, Block and Release for a lot manager. */
const HoldsPanel = ({ lot, manage, moduleLabel }: { lot: InventoryLot; manage: boolean; moduleLabel: ModuleLabel }) => {
  const { t, stamp } = useInventoryFormat();
  const [putting, setPutting] = useState<InventoryLotHoldKind>();
  const [releasing, setReleasing] = useState<InventoryLotHold>();
  const held = { quarantine: pageHolds(lot, "quarantine"), block: pageHolds(lot, "block") };
  return (
    <PageSection>
      <Stack gap="sm" data-assist="inventory.lot.holds">
        <Group justify="space-between">
          <Title order={4}>{t("holdsTitle")}</Title>
          {manage && (
            <Group gap="xs">
              <Button
                size="xs"
                variant="light"
                color="orange"
                leftSection={<IconShieldPause size={14} />}
                disabled={held.quarantine}
                onClick={() => setPutting("quarantine")}
              >
                {t("quarantineLot")}
              </Button>
              <Button
                size="xs"
                variant="light"
                color="red"
                leftSection={<IconLock size={14} />}
                disabled={held.block}
                onClick={() => setPutting("block")}
              >
                {t("blockLot")}
              </Button>
            </Group>
          )}
        </Group>
        {manage &&
          holdKinds
            .filter((kind) => held[kind])
            .map((kind) => (
              <Text key={kind} size="xs" c="dimmed">
                {t(`holdExists_${kind}`)}
              </Text>
            ))}
        {lot.holds.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noHolds")}
          </Text>
        ) : (
          <Table verticalSpacing="xs">
            <Table.Tbody>
              {lot.holds.map((hold) => (
                <Table.Tr key={hold.id} data-hold={hold.id} c={hold.releasedAt ? "dimmed" : undefined}>
                  <Table.Td w={140}>
                    <Badge
                      variant={hold.releasedAt ? "outline" : "light"}
                      color={hold.releasedAt ? "gray" : hold.kind === "block" ? "red" : "orange"}
                    >
                      {t(`holdKind_${hold.kind}`)}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Stack gap={2}>
                      <Text size="sm">
                        {t(`holdCause_${hold.cause}`)}
                        {hold.note ? ` — ${hold.note}` : ""}
                      </Text>
                      <Text size="xs" c="dimmed">
                        <SourceLink source={hold.source} lotId={lot.id} moduleLabel={moduleLabel} />
                        {" · "}
                        {t("heldByAt", { name: hold.createdBy?.displayName ?? "—", when: stamp(hold.createdAt) })}
                      </Text>
                      {hold.releasedAt && (
                        <Text size="xs" c="dimmed">
                          {t("releasedByAt", {
                            name: hold.releasedBy?.displayName ?? "—",
                            when: stamp(hold.releasedAt),
                          })}
                          {hold.releaseNote ? ` — ${hold.releaseNote}` : ""}
                        </Text>
                      )}
                    </Stack>
                  </Table.Td>
                  <Table.Td ta="right" w={180}>
                    {!hold.releasedAt &&
                      (hold.inventorys ? (
                        manage && (
                          <Button size="compact-xs" variant="subtle" onClick={() => setReleasing(hold)}>
                            {t("releaseHold")}
                          </Button>
                        )
                      ) : (
                        <Text size="xs" c="dimmed">
                          {t("holdSourceElsewhere", { module: moduleLabel(hold.source.module) })}
                        </Text>
                      ))}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
      {putting === "quarantine" && <HoldDialog lot={lot} kind={putting} onClose={() => setPutting(undefined)} />}
      {putting === "block" && <BlockDialog lot={lot} onClose={() => setPutting(undefined)} />}
      {releasing && <ReleaseDialog lot={lot} hold={releasing} onClose={() => setReleasing(undefined)} />}
    </PageSection>
  );
};

/** A lot's files (M54): each downloads; a lot manager adds a PDF, JPEG or PNG and removes one. */
const FilesPanel = ({ lot, manage }: { lot: InventoryLot; manage: boolean }) => {
  const { t, stamp } = useInventoryFormat();
  const queryClient = useQueryClient();
  const [removing, setRemoving] = useState<InventoryLot["attachments"][number]>();
  const [problem, setProblem] = useState<string>();
  const refresh = () => void queryClient.invalidateQueries({ queryKey: [INVENTORY_QUERY_KEY, "lot", lot.id] });
  const uploading = useMutation({
    mutationFn: (file: File) => uploadLotAttachment(lot.id, file),
    onSuccess: () => {
      refresh();
      notifications.show({ color: "teal", message: t("fileAdded") });
    },
    onError: (error: Error) => setProblem(refusalMessage(t, error)),
  });
  const deleting = useMutation({
    mutationFn: (id: number) => deleteLotAttachment(lot.id, id),
    onSuccess: () => {
      refresh();
      setRemoving(undefined);
      notifications.show({ color: "teal", message: t("fileRemoved") });
    },
  });
  const chosen = (file: File | null) => {
    setProblem(undefined);
    if (!file) return;
    if (file.size > MAX_LOT_FILE_BYTES) return setProblem(t("fileTooLarge"));
    if (file.type && !(LOT_FILE_TYPES as readonly string[]).includes(file.type)) return setProblem(t("fileWrongType"));
    uploading.mutate(file);
  };
  return (
    <PageSection>
      <Stack gap="sm">
        <Group justify="space-between">
          <Title order={4}>{t("filesTitle")}</Title>
          {manage && (
            <FileButton onChange={chosen} accept={LOT_FILE_TYPES.join(",")}>
              {(props) => (
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconUpload size={14} />}
                  loading={uploading.isPending}
                  {...props}
                >
                  {t("addFile")}
                </Button>
              )}
            </FileButton>
          )}
        </Group>
        {manage && (
          <Text size="xs" c="dimmed">
            {t("fileHint")}
          </Text>
        )}
        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {problem}
          </Alert>
        )}
        {lot.attachments.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noFiles")}
          </Text>
        ) : (
          <Table verticalSpacing="xs">
            <Table.Tbody>
              {lot.attachments.map((file) => (
                <Table.Tr key={file.id}>
                  <Table.Td>
                    <Anchor size="sm" href={lotAttachmentHref(lot.id, file.id)} download={file.fileName}>
                      {file.fileName}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">
                      {t("uploadedByAt", { name: file.uploadedBy?.displayName ?? "—", when: stamp(file.uploadedAt) })}
                    </Text>
                  </Table.Td>
                  <Table.Td ta="right" w={90}>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        component="a"
                        href={lotAttachmentHref(lot.id, file.id)}
                        download={file.fileName}
                        aria-label={file.fileName}
                      >
                        <IconDownload size={16} />
                      </ActionIcon>
                      {manage && (
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          aria-label={t("removeFile")}
                          onClick={() => setRemoving(file)}
                        >
                          <IconTrash size={16} />
                        </ActionIcon>
                      )}
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
      {removing && (
        <Modal opened onClose={() => setRemoving(undefined)} title={t("removeFileTitle")}>
          <Stack gap="sm">
            <Text size="sm">{t("removeFileBody", { name: removing.fileName })}</Text>
            {deleting.error && (
              <Alert color="red" icon={<IconAlertCircle size={16} />}>
                {refusalMessage(t, deleting.error)}
              </Alert>
            )}
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setRemoving(undefined)}>
                {t("cancel")}
              </Button>
              <Button color="red" loading={deleting.isPending} onClick={() => deleting.mutate(removing.id)}>
                {t("removeFile")}
              </Button>
            </Group>
          </Stack>
        </Modal>
      )}
    </PageSection>
  );
};

/**
 * A lot's page (manufacturing phase 2 M61): its number, item, supplier lot,
 * dates, status and the document that made it; its holds (M64), where it
 * lies, its movement lines newest first, and its files (M54). Edit,
 * Quarantine, Block, Release and the files' writes need
 * `inventory:lots-manage`. The page refetches on the lot's live topic and
 * polls while it is down.
 */
export const LotPage = ({ moduleLabel = ownKey }: { moduleLabel?: ModuleLabel }) => {
  const { t, quantity, stamp, day } = useInventoryFormat();
  const { lotId: rawId } = useParams({ strict: false }) as { lotId: string };
  const lotId = Number(rawId);
  const live = useLotsLive(lotId);
  const meta = useQuery(inventoryMetaQueryOptions());
  const lot = useQuery(lotQueryOptions(lotId, live));
  const data = lot.data;
  const manage = canManageLots(meta.data);
  const reportDeviation = canViewLots(meta.data) ? reportDeviationHref(lotId) : undefined;
  const [editing, setEditing] = useState(false);
  const [printing, setPrinting] = useState(false);

  return (
    <Stack gap="lg">
      {data && (
        <PrintLotLabelsDialog
          opened={printing}
          onClose={() => setPrinting(false)}
          lotIds={[data.id]}
          name={data.number}
        />
      )}
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("lotsTitle"), to: LOTS_ROUTE_PATH },
          { label: data?.number ?? t("lotById", { id: lotId }) },
        ]}
        title={
          <Group gap="sm">
            <span>{data ? t("lotTitle", { number: data.number }) : t("lotById", { id: lotId })}</span>
            {data && <LotStatusBadges status={data.status} expired={data.expired} />}
            {data?.serial && (
              <Badge variant="outline" color="gray">
                {t("serial")}
              </Badge>
            )}
          </Group>
        }
        description={data ? `${data.variant.sku} · ${data.variant.productName}` : undefined}
        actions={
          data && (
            <Group gap="xs">
              <Button
                variant="default"
                leftSection={<IconRoute size={16} />}
                renderRoot={(props) => <Link {...traceLinkOptions(data.id)} {...props} />}
              >
                {t("traceLot")}
              </Button>
              <Button variant="default" leftSection={<IconPrinter size={16} />} onClick={() => setPrinting(true)}>
                {t("printLotLabels")}
              </Button>
              {manage && (
                <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
                  {t("editLot")}
                </Button>
              )}
              {reportDeviation && (
                <Button
                  variant="default"
                  color="red"
                  leftSection={<IconZoomExclamation size={16} />}
                  data-assist="inventory.lot.report-deviation"
                  renderRoot={(props) => <Link to={reportDeviation as never} {...props} />}
                >
                  {t("reportDeviation")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {lot.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {lot.error.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={5} />
      ) : (
        <>
          <PageSection>
            <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
              <Fact label={t("variant")}>
                <Anchor
                  size="sm"
                  renderRoot={(props) => <Link {...variantStockLinkOptions(data.variant.id)} {...props} />}
                >
                  {data.variant.sku}
                </Anchor>
              </Fact>
              <Fact label={t("totalOnHand")}>{quantity(data.onHand, data.variant.unit)}</Fact>
              <Fact label={t("supplierLot")}>{data.supplierLot ?? "—"}</Fact>
              <Fact label={t("firstReceived")}>{stamp(data.receivedAt)}</Fact>
              <Fact label={t("productionDate")}>{data.productionDate ? day(data.productionDate) : "—"}</Fact>
              <Fact label={t("bestBefore")}>{data.bestBefore ? day(data.bestBefore) : "—"}</Fact>
              <Fact label={t("expiryDate")}>
                <Text size="sm" c={data.expired ? "red" : undefined} span>
                  {data.expiry ? day(data.expiry) : "—"}
                </Text>
              </Fact>
              {data.origin && (
                <Fact label={t("madeBy")}>
                  <SourceLink source={data.origin} lotId={data.id} moduleLabel={moduleLabel} />
                </Fact>
              )}
            </SimpleGrid>
            {data.note && (
              <Text size="sm" mt="md" style={{ whiteSpace: "pre-wrap" }}>
                {data.note}
              </Text>
            )}
          </PageSection>
          <HoldsPanel lot={data} manage={manage} moduleLabel={moduleLabel} />
          <PageSection>
            <Stack gap="sm">
              <Title order={4}>{t("whereItLies")}</Title>
              {data.locations.length === 0 ? (
                <EmptyState size="sm" title={t("noStock")} />
              ) : (
                <Table striped verticalSpacing="xs">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("columnSite")}</Table.Th>
                      <Table.Th>{t("columnLocation")}</Table.Th>
                      <Table.Th ta="right">{t("columnOnHand")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {data.locations.map((row) => (
                      <Table.Tr key={row.location.id}>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...siteLinkOptions(row.site.id)} {...props} />}
                          >
                            {row.site.code}
                          </Anchor>
                        </Table.Td>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            ff="monospace"
                            renderRoot={(props) => <Link {...locationLinkOptions(row.location.id)} {...props} />}
                          >
                            {row.location.fullCode}
                          </Anchor>
                        </Table.Td>
                        <Table.Td ta="right">{quantity(row.onHand, data.variant.unit)}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Stack>
          </PageSection>
          <PageSection>
            <Stack gap="sm">
              <Title order={4}>{t("navMovements")}</Title>
              {data.movements.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noMovements")}
                </Text>
              ) : (
                <Table.ScrollContainer minWidth={560}>
                  <Table striped verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("columnMovement")}</Table.Th>
                        <Table.Th>{t("columnPostedAt")}</Table.Th>
                        <Table.Th>{t("columnFrom")}</Table.Th>
                        <Table.Th>{t("columnTo")}</Table.Th>
                        <Table.Th ta="right">{t("columnQuantity")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {data.movements.map((line) => (
                        <Table.Tr key={`${line.movementId}-${line.lineNo}`}>
                          <Table.Td>
                            <Group gap={6} wrap="nowrap">
                              <Anchor
                                size="sm"
                                renderRoot={(props) => <Link {...movementLinkOptions(line.movementId)} {...props} />}
                              >
                                {t("movementNumber", { id: line.movementId })}
                              </Anchor>
                              <Badge size="xs" variant="light" color={movementKindColor[line.kind]}>
                                {t(`kind_${line.kind}`)}
                              </Badge>
                            </Group>
                          </Table.Td>
                          <Table.Td>{stamp(line.postedAt)}</Table.Td>
                          <Table.Td ff="monospace">{line.from?.fullCode ?? "—"}</Table.Td>
                          <Table.Td ff="monospace">{line.to?.fullCode ?? "—"}</Table.Td>
                          <Table.Td ta="right">{quantity(line.quantity, data.variant.unit)}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}
              {data.movements.length >= LOT_PAGE_MOVEMENTS && (
                <Text size="xs" c="dimmed">
                  {t("movementsLatest", { count: LOT_PAGE_MOVEMENTS })}
                </Text>
              )}
            </Stack>
          </PageSection>
          <FilesPanel lot={data} manage={manage} />
          {editing && <EditLotDialog lot={data} onClose={() => setEditing(false)} />}
        </>
      )}
    </Stack>
  );
};

import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Box,
  Button,
  Group,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Text,
} from "@mantine/core";
import { IconAlertCircle, IconChevronDown, IconChevronRight, IconDownload, IconRoute } from "@tabler/icons-react";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { downloadBlob } from "../api/labels";
import { lotsInfiniteQueryOptions } from "../api/lots";
import {
  exportTrace,
  type InventoryTrace,
  type InventoryTraceNode,
  type TraceParams,
  traceQueryOptions,
} from "../api/trace";
import { LotStatusBadges } from "../components/lot-status";
import { movementKindColor, refusalMessage, useInventoryFormat } from "../lib/format";
import {
  INVENTORY_HOME_ROUTE_PATH,
  LOTS_ROUTE_PATH,
  lotLinkOptions,
  movementLinkOptions,
  sourceHref,
  TRACE_ROUTE_PATH,
} from "../lib/routes";
import type { TraceSearch } from "../lib/search";

/** Whether a node lies under a collapsed node: its key starts with the collapsed key and a dot. */
const hiddenBy = (node: Pick<InventoryTraceNode, "key">, collapsed: ReadonlySet<string>) => {
  for (const key of collapsed) if (node.key.startsWith(`${key}.`)) return true;
  return false;
};

/** The keys that have children: a lot that was expanded. */
const parentsOf = (trace: InventoryTrace) => new Set(trace.nodes.flatMap((node) => node.parentKey ?? []));

/** The id of a node's row, which an *Already shown above* link jumps to. */
const rowId = (key: string) => `trace-node-${key}`;

/** A row's height before it is measured, and how many rows are drawn beyond the window. */
const ROW_HEIGHT_ESTIMATE = 56;
const ROWS_OVERSCAN = 12;

/** The collapsed keys without those above `key`: what is folded once the path to `key` is opened. */
const openPathTo = (collapsed: ReadonlySet<string>, key: string) =>
  new Set([...collapsed].filter((folded) => !key.startsWith(`${folded}.`)));

/** One node of the tree: a lot with its production, an untracked component, or the movement that ends a path. */
const TraceRow = ({
  node,
  trace,
  open,
  hasChildren,
  onToggle,
  onJump,
}: {
  node: InventoryTraceNode;
  trace: InventoryTrace;
  open: boolean;
  hasChildren: boolean;
  onToggle: () => void;
  /** Opens the path to a node and scrolls to it: where a lot traced already was first shown. */
  onJump: (key: string) => void;
}) => {
  const { t, quantity, stamp } = useInventoryFormat();
  const unit = node.variant.unit;
  const item = `${node.variant.sku} · ${node.variant.productName}`;
  const usage = trace.direction === "usage";
  const production = node.edge && (
    <Text size="xs" c="dimmed">
      {(() => {
        const href = sourceHref(node.edge.source ? { ...node.edge.source } : undefined);
        const label = t("traceProduction", { id: node.edge.source?.id ?? node.edge.movementId });
        return href ? (
          <Anchor size="xs" href={href}>
            {label}
          </Anchor>
        ) : (
          label
        );
      })()}
      {" · "}
      {stamp(node.edge.postedAt)}
      {" · "}
      {t("traceUsedMade", { used: quantity(node.edge.consumed), made: quantity(node.edge.produced) })}
    </Text>
  );
  let body: ReactNode;
  if (node.kind === "lot" && node.lot) {
    const lot = node.lot;
    body = (
      <Stack gap={2}>
        <Group gap={6} wrap="wrap">
          <Anchor
            size="sm"
            fw={600}
            ff="monospace"
            renderRoot={(props) => <Link {...lotLinkOptions(lot.id)} {...props} />}
          >
            {lot.number}
          </Anchor>
          <Text size="sm">{item}</Text>
          {lot.status !== "available" && <LotStatusBadges status={lot.status} expired={false} />}
          <Text size="xs" c="dimmed">
            {t("traceInStock", { quantity: quantity(lot.onHand, unit) })}
          </Text>
        </Group>
        {production}
        {node.seenAt && (
          <Text size="xs" c="dimmed">
            <Anchor
              size="xs"
              href={`#${rowId(node.seenAt)}`}
              onClick={(event) => {
                event.preventDefault();
                if (node.seenAt) onJump(node.seenAt);
              }}
            >
              {t("traceAlreadyShown")}
            </Anchor>
          </Text>
        )}
        {node.depthLimit && (
          <Text size="xs" c="dimmed">
            {t("traceDepthLimit")}
          </Text>
        )}
      </Stack>
    );
  } else if (node.kind === "component") {
    body = (
      <Stack gap={2}>
        <Group gap={6}>
          <Badge size="xs" variant="outline" color="gray">
            {t("traceComponent")}
          </Badge>
          <Text size="sm">{item}</Text>
        </Group>
        {production}
      </Stack>
    );
  } else {
    const movement = node.movement;
    const delivery = node.delivery;
    const label =
      node.kind === "unbuild"
        ? t(usage ? "traceUnbuilt" : "traceReturnedByUnbuild")
        : node.kind === "transfer_out" && movement?.toSiteName
          ? t("traceSentTo", { site: movement.toSiteName })
          : t(`kind_${node.kind}`);
    body = (
      <Stack gap={2}>
        <Group gap={6} wrap="wrap">
          <Badge
            size="xs"
            variant="light"
            color={movementKindColor[node.kind as keyof typeof movementKindColor] ?? "gray"}
          >
            {label}
          </Badge>
          {movement && (
            <Anchor size="sm" renderRoot={(props) => <Link {...movementLinkOptions(movement.id)} {...props} />}>
              {t("movementNumber", { id: movement.id })}
            </Anchor>
          )}
          {node.kind === "unbuild" &&
            (() => {
              // The unbuild's page in Manufacturing, which names it UB-n (M123).
              const href = sourceHref(movement?.source ? { ...movement.source } : undefined);
              return href ? (
                <Anchor size="sm" href={href}>
                  {t("traceUnbuildLink")}
                </Anchor>
              ) : null;
            })()}
          {node.quantity !== undefined && <Text size="sm">{quantity(node.quantity, unit)}</Text>}
          {movement && (
            <Text size="xs" c="dimmed">
              {stamp(movement.postedAt)}
            </Text>
          )}
        </Group>
        {node.kind === "receipt" && movement?.supplierName && (
          <Text size="xs" c="dimmed">
            {t("traceSupplier", { name: movement.supplierName })}
            {movement.supplierReference ? ` · ${movement.supplierReference}` : ""}
          </Text>
        )}
        {node.kind === "issue" && delivery && (
          <Text size="xs" c="dimmed">
            {t("traceDelivery", { delivery: delivery.deliveryNumber, order: delivery.orderNumber })}
            {" · "}
            {delivery.customerName ?? t("traceCustomerId", { id: delivery.customerId })}
          </Text>
        )}
        {node.kind === "issue" && !delivery && movement?.source?.module === "orders" && (
          <Text size="xs" c="dimmed">
            {t("traceDeliveryMovementOnly")}
          </Text>
        )}
      </Stack>
    );
  }
  return (
    <Group
      id={rowId(node.key)}
      gap={4}
      wrap="nowrap"
      align="flex-start"
      pl={node.depth * 24}
      py={4}
      data-trace-key={node.key}
      style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}
    >
      <Box w={24}>
        {hasChildren && (
          <ActionIcon
            size="sm"
            variant="subtle"
            aria-label={t(open ? "traceCollapse" : "traceExpand", { key: node.lot?.number ?? node.key })}
            onClick={onToggle}
          >
            {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
          </ActionIcon>
        )}
      </Box>
      {body}
    </Group>
  );
};

/** The lot picker shown before a lot is chosen: a lot number, a supplier lot or an SKU, searched in the lots list. */
const LotPicker = ({ onPick }: { onPick: (lotId: number) => void }) => {
  const { t } = useInventoryFormat();
  const [q, setQ] = useState("");
  const lots = useInfiniteQuery({
    ...lotsInfiniteQueryOptions({ q: q.trim() || undefined, withStock: false }, true, 20),
    enabled: q.trim().length > 0,
  });
  const rows = lots.data?.pages.flatMap((page) => page.lots) ?? [];
  return (
    <Select
      label={t("tracePickLot")}
      description={t("tracePickLotHint")}
      searchable
      searchValue={q}
      onSearchChange={setQ}
      filter={({ options }) => options}
      nothingFoundMessage={q.trim() ? t("noLots") : undefined}
      data={rows.map((lot) => ({
        value: String(lot.id),
        label: `${lot.number} · ${lot.variant.sku} · ${lot.variant.productName}`,
      }))}
      value={null}
      onChange={(next) => next && onPick(Number(next))}
      maw={480}
    />
  );
};

/**
 * A lot's trace (manufacturing phase 2 M87–M91): where it went (usage) or
 * where it came from (origin), through the productions that used or made
 * it, down to the receipts and the deliveries. A lot reached twice is shown
 * once and referenced after (*Already shown above*); a lot's branch folds
 * away. Origin may list the untracked components each production used. The
 * tree exports as CSV and PDF. Who received the lot comes from Orders, for a
 * caller who may see its orders; the customers' names only for one who may
 * see customers. A stock read: both views.
 */
export const TracePage = () => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as TraceSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const params: TraceParams | undefined = search.lotId
    ? { lotId: search.lotId, direction: search.direction ?? "usage", components: search.components ?? "tracked" }
    : undefined;
  const trace = useQuery({ ...traceQueryOptions(params ?? { lotId: 0 }), enabled: params !== undefined });
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const go = (patch: Partial<TraceSearch>) => {
    setCollapsed(new Set());
    void navigate({ to: TRACE_ROUTE_PATH, search: { ...search, ...patch }, replace: true });
  };
  const exporting = useMutation({
    mutationFn: async (format: "csv" | "pdf") => {
      if (!params) return;
      const blob = await exportTrace(params, format);
      downloadBlob(blob, `trace-${root?.lot?.number ?? params.lotId}-${params.direction}.${format}`);
    },
  });
  const data = trace.data;
  const root = data?.nodes[0];
  const parents = data ? parentsOf(data) : new Set<string>();
  const toggle = (key: string) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  // The rows shown, drawn as the window reaches them: a trace may have thousands.
  const shown = data ? data.nodes.filter((node) => !hiddenBy(node, collapsed)) : [];
  const [tree, setTree] = useState<HTMLDivElement | null>(null);
  const virtualizer = useWindowVirtualizer({
    count: shown.length,
    estimateSize: () => ROW_HEIGHT_ESTIMATE,
    overscan: ROWS_OVERSCAN,
    scrollMargin: tree ? tree.getBoundingClientRect().top + window.scrollY : 0,
    getItemKey: (index) => shown[index]?.key ?? index,
    measureElement: (element) => element.getBoundingClientRect().height || ROW_HEIGHT_ESTIMATE,
  });
  const drawn = virtualizer.getVirtualItems();
  const margin = virtualizer.options.scrollMargin;
  const spaceAbove = drawn.length > 0 ? drawn[0].start - margin : 0;
  const spaceBelow = drawn.length > 0 ? virtualizer.getTotalSize() - (drawn[drawn.length - 1].end - margin) : 0;
  // *Already shown above*: open the path to the row, then, after that render, scroll to it.
  const pendingJump = useRef<string | undefined>(undefined);
  const jump = (key: string) => {
    pendingJump.current = key;
    setCollapsed((current) => openPathTo(current, key));
  };
  useEffect(() => {
    const key = pendingJump.current;
    if (key === undefined) return;
    const index = shown.findIndex((node) => node.key === key);
    if (index < 0) return;
    pendingJump.current = undefined;
    virtualizer.scrollToIndex(index, { align: "start" });
  });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("lotsTitle"), to: LOTS_ROUTE_PATH },
          { label: t("traceTitle") },
        ]}
        title={root?.lot ? t("traceOfLot", { number: root.lot.number }) : t("traceTitle")}
        description={root ? `${root.variant.sku} · ${root.variant.productName}` : t("traceDescription")}
        actions={
          params && (
            <Group gap="xs">
              <Button
                variant="default"
                leftSection={<IconDownload size={16} />}
                loading={exporting.isPending && exporting.variables === "csv"}
                onClick={() => exporting.mutate("csv")}
              >
                {t("traceExportCsv")}
              </Button>
              <Button
                variant="default"
                leftSection={<IconDownload size={16} />}
                loading={exporting.isPending && exporting.variables === "pdf"}
                onClick={() => exporting.mutate("pdf")}
              >
                {t("traceExportPdf")}
              </Button>
            </Group>
          )
        }
      />
      <PageSection>
        <Stack gap="md" data-assist="inventory.trace.tree">
          {!params ? (
            <LotPicker onPick={(lotId) => go({ lotId })} />
          ) : (
            <Group gap="lg" wrap="wrap">
              <SegmentedControl
                aria-label={t("traceDirection")}
                value={params.direction ?? "usage"}
                onChange={(value) => go({ direction: value === "origin" ? "origin" : undefined })}
                data={[
                  { value: "usage", label: t("traceDirection_usage") },
                  { value: "origin", label: t("traceDirection_origin") },
                ]}
              />
              {params.direction === "origin" && (
                <Switch
                  label={t("traceComponents")}
                  checked={params.components === "all"}
                  onChange={(event) => go({ components: event.currentTarget.checked ? "all" : undefined })}
                />
              )}
            </Group>
          )}
          {exporting.error && (
            <Alert color="red" icon={<IconAlertCircle size={16} />}>
              {refusalMessage(t, exporting.error)}
            </Alert>
          )}
          {data && data.deliveries !== "shown" && (
            <Alert color="gray" icon={<IconAlertCircle size={16} />}>
              {t(`traceDeliveries_${data.deliveries}`)}
            </Alert>
          )}
          {!params ? (
            <EmptyState size="sm" icon={IconRoute} title={t("traceNoLot")} />
          ) : trace.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {refusalMessage(t, trace.error)}
            </Alert>
          ) : !data ? (
            <ContentSkeleton rows={5} />
          ) : (
            <Stack gap={0} data-testid="trace-tree" ref={setTree}>
              {spaceAbove > 0 && <div style={{ height: spaceAbove }} aria-hidden />}
              {drawn.map((row) => {
                const node = shown[row.index];
                if (!node) return null;
                return (
                  <div key={row.key} data-index={row.index} ref={virtualizer.measureElement}>
                    <TraceRow
                      node={node}
                      trace={data}
                      hasChildren={parents.has(node.key)}
                      open={!collapsed.has(node.key)}
                      onToggle={() => toggle(node.key)}
                      onJump={jump}
                    />
                  </div>
                );
              })}
              {spaceBelow > 0 && <div style={{ height: spaceBelow }} aria-hidden />}
              {data.nodes.length === 1 && (
                <Text size="sm" c="dimmed" mt="sm">
                  {t(data.direction === "usage" ? "traceNothingUsage" : "traceNothingOrigin")}
                </Text>
              )}
            </Stack>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};

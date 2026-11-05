import {
  Alert,
  Anchor,
  Badge,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Radio,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  TextInput,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck, IconScan, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useBlocker, useParams } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  cancelCount,
  countQueryOptions,
  type InventoryCount,
  type InventoryCountLine,
  type InventoryCountLineInput,
  type InventoryUncounted,
  MAX_COUNT_LINES_PER_SAVE,
  postCount,
  putCountLines,
} from "../api/counts";
import type { InventoryLocationRef } from "../api/locations";
import { siteLocationListQueryOptions } from "../api/locations";
import { lookupQueryOptions } from "../api/lookup";
import { inventoryMetaQueryOptions } from "../api/meta";
import { refusalCodes } from "../api/movements";
import { refusalCode, refusedLines } from "../api/request";
import type { InventoryVariantRef } from "../api/stock";
import { PackagingSteppers } from "../components/packaging-steppers";
import { Quantity } from "../components/quantity";
import { subtreeOf } from "../lib/actions";
import { packedQuantity } from "../lib/breakdown";
import { canCount } from "../lib/capabilities";
import {
  countStatusColor,
  QUANTITY_DECIMALS,
  refusalMessage,
  useInvalidateInventory,
  useInventoryFormat,
} from "../lib/format";
import { enteredOf, roundQuantity } from "../lib/lines";
import { COUNTS_ROUTE_PATH, INVENTORY_HOME_ROUTE_PATH, movementLinkOptions } from "../lib/routes";

/** How long the screen waits after the last keystroke before it saves what was counted. */
const SAVE_DELAY_MS = 800;

/** A line's height before it is drawn, and in a test's DOM, which has no layout. */
const LINE_HEIGHT_ESTIMATE = 64;
/** How many lines are drawn beyond the window's edges, so Tab reaches the next one. */
const LINES_OVERSCAN = 10;

/** What the person has counted on a line and not yet saved. */
interface Draft {
  counted: number | "";
  packs: Readonly<Record<number, number>>;
}

/** A line of the screen: one the count has, or one the person scanned where nothing was expected (I18). */
interface Row {
  key: string;
  variant: InventoryVariantRef;
  location: InventoryLocationRef;
  line?: InventoryCountLine;
}

type Marks = ReadonlyMap<string, { message: string; detail?: string }>;

/** Posting waited on a save that failed: the save's error is already on screen. */
class NotSavedError extends Error {}

const keyOf = (variantId: number, locationId: number) => `${variantId}:${locationId}`;

/**
 * A saved line as the screen edits it: its packagings and loose rest when it
 * was entered so, else its count. An entered line's loose rest is 0 when the
 * server left it out, so stepping every packaging down to 0 counts 0 rather
 * than reading as an emptied field.
 */
const savedDraft = (line?: InventoryCountLine): Draft =>
  line?.entered
    ? {
        counted: line.entered.loose ?? 0,
        packs: Object.fromEntries(line.entered.packagings.map((one) => [one.packagingId, one.count])),
      }
    : { counted: line?.counted ?? "", packs: {} };

const isPackedDraft = (draft: Draft) => Object.values(draft.packs).some((count) => count > 0);

/** What a draft counts in the variant's unit; empty while nothing is filled in. */
const draftTotal = (variant: InventoryVariantRef, draft: Draft): number | "" =>
  isPackedDraft(draft)
    ? packedQuantity(variant.packagings ?? [], draft.packs, Number(draft.counted || 0))
    : draft.counted;

/** A draft as `PUT /counts/{id}/lines` takes it: in packagings when one is counted, else the counted quantity. */
const lineInput = (row: Row, draft: Draft): InventoryCountLineInput => ({
  variantId: row.variant.id,
  locationId: row.location.id,
  ...(isPackedDraft(draft)
    ? { entered: enteredOf(draft.packs, draft.counted) }
    : { counted: roundQuantity(Number(draft.counted)) }),
});

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

/**
 * The counting screen (I18), and its review (I19). Lines are ordered by the
 * location's full code. Scanning a location — its label's LOC token or its
 * full code — makes it the one being counted; scanning an item there focuses
 * its line, or adds one where nothing was expected (`added`), and a
 * packaging's barcode counts one of that packaging. Counted quantities are
 * typed or stepped, and saved by themselves a moment after the last change,
 * at most 500 lines a request. Review lists the differences against the
 * balance now, the lines whose balance moved since the count opened — which
 * posting needs accepted — and what to do with the lines nobody counted.
 */
const CountingScreen = ({ count, savesRef }: { count: InventoryCount; savesRef: RefObject<Promise<boolean>> }) => {
  const { t, quantity, number } = useInventoryFormat();
  const queryClient = useQueryClient();
  const invalidate = useInvalidateInventory();
  const [drafts, setDrafts] = useState<ReadonlyMap<string, Draft>>(new Map());
  const [added, setAdded] = useState<readonly Row[]>([]);
  const [current, setCurrent] = useState<InventoryLocationRef>();
  const [onlyCurrent, setOnlyCurrent] = useState(true);
  const [scan, setScan] = useState("");
  const [scanError, setScanError] = useState<string>();
  // Each scan asks anew, so scanning the same item twice focuses it twice.
  const [focusRequest, setFocusRequest] = useState<{ key: string }>();
  const focused = useRef<{ key: string }>(undefined);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string>();
  const [marks, setMarks] = useState<Marks>(new Map());
  const [uncounted, setUncounted] = useState<InventoryUncounted>("keep");
  const [acceptMoved, setAcceptMoved] = useState(false);
  const [tab, setTab] = useState<string | null>("count");
  const [postError, setPostError] = useState<string>();
  const scanRef = useRef<HTMLInputElement>(null);
  const draftsRef = useRef(drafts);
  useEffect(() => {
    draftsRef.current = drafts;
  }, [drafts]);
  const scopeIsSite = count.scope.kind === "site";
  // A scanned location must lie in the count's scope: the site's tree says which do. Read ahead, so a scan rarely waits.
  useQuery({ ...siteLocationListQueryOptions(count.site.id, "active"), enabled: !scopeIsSite });
  // Saves in flight, so leaving waits for them too.
  const busy = useRef(0);
  const countKey = useMemo(() => countQueryOptions(count.id).queryKey, [count.id]);

  const lines = useMemo(() => count.lines ?? [], [count.lines]);
  const rows = useMemo(() => {
    const known = new Map<string, Row>(
      lines.map((line) => {
        const key = keyOf(line.variant.id, line.location.id);
        return [key, { key, variant: line.variant, location: line.location, line }];
      }),
    );
    for (const row of added) if (!known.has(row.key)) known.set(row.key, row);
    return [...known.values()].sort(
      (a, b) => a.location.fullCode.localeCompare(b.location.fullCode) || a.variant.sku.localeCompare(b.variant.sku),
    );
  }, [lines, added]);

  const draftOf = (row: Row) => drafts.get(row.key) ?? savedDraft(row.line);
  const edit = (row: Row, change: Partial<Draft>) =>
    setDrafts((now) => new Map(now).set(row.key, { ...(now.get(row.key) ?? savedDraft(row.line)), ...change }));

  // Forgets saved or emptied drafts, in the ref at once too, so a save chained right behind reads what is left.
  const forget = useCallback((done: (key: string, draft: Draft) => boolean) => {
    const without = (now: ReadonlyMap<string, Draft>) => {
      const next = new Map(now);
      for (const [key, draft] of now) if (done(key, draft)) next.delete(key);
      return next;
    };
    draftsRef.current = without(draftsRef.current);
    setDrafts(without);
  }, []);

  // Saves every draft that holds a count, 500 lines a request; a draft changed while its request was out stays.
  const saveNow = useCallback(async (): Promise<boolean> => {
    const emptied = new Set<string>();
    const pending = [...draftsRef.current].filter(([key, draft]) => {
      if (draft.counted !== "" || isPackedDraft(draft)) return true;
      // A field emptied cannot uncount a line: it goes back to what was saved.
      emptied.add(key);
      return false;
    });
    if (emptied.size > 0) forget((key, draft) => emptied.has(key) && draft.counted === "" && !isPackedDraft(draft));
    if (pending.length === 0) return true;
    const byKey = new Map(rows.map((row) => [row.key, row]));
    setSaving(true);
    setSaveError(undefined);
    try {
      for (let at = 0; at < pending.length; at += MAX_COUNT_LINES_PER_SAVE) {
        const batch = pending.slice(at, at + MAX_COUNT_LINES_PER_SAVE).filter(([key]) => byKey.has(key));
        try {
          const answer = await putCountLines(
            count.id,
            batch.map(([key, draft]) => lineInput(byKey.get(key) as Row, draft)),
          );
          // An answer older than what the screen holds — a Post or a Cancel landed first — is not put back.
          const cached = queryClient.getQueryData(countKey);
          if (!cached || answer.revision >= cached.revision) queryClient.setQueryData(countKey, answer);
          const saved = new Map(batch);
          forget((key, draft) => saved.get(key) === draft);
          setMarks(new Map());
        } catch (error) {
          const failing = refusedLines(error);
          const next = new Map<string, { message: string; detail?: string }>();
          for (const one of failing) {
            const entry = batch[one.index];
            if (!entry) continue;
            next.set(
              entry[0],
              (refusalCodes as readonly string[]).includes(one.code)
                ? { message: t(`refusal.${one.code}`), detail: one.detail }
                : { message: one.detail },
            );
          }
          setMarks(next);
          setSaveError(
            failing.length > 0 ? t("countLinesRefused", { count: failing.length }) : refusalMessage(t, error as Error),
          );
          if (refusalCode(error) === "count_not_open") await invalidate();
          return false;
        }
      }
      return true;
    } finally {
      setSaving(false);
    }
  }, [count.id, countKey, queryClient, rows, t, invalidate, forget]);

  // One save at a time: each waits for the one before, and Post and Cancel wait for the last (`savesRef`).
  const save = useCallback((): Promise<boolean> => {
    busy.current += 1;
    const run = (savesRef.current ?? Promise.resolve(true)).then(saveNow).finally(() => {
      busy.current -= 1;
    });
    savesRef.current = run.catch(() => false);
    return run;
  }, [saveNow, savesRef]);

  // Leaving with unsaved counts saves them first; only what cannot be saved is asked about, and closing the tab warns.
  useBlocker({
    shouldBlockFn: async () => {
      if (draftsRef.current.size === 0 && busy.current === 0) return false;
      if (await save()) return false;
      const leave = await new Promise<boolean>((resolve) =>
        modals.openConfirmModal({
          title: t("leaveCountTitle"),
          children: <Text size="sm">{t("leaveCountBody")}</Text>,
          labels: { confirm: t("leaveCount"), cancel: t("keepCounting") },
          confirmProps: { color: "red" },
          onConfirm: () => resolve(true),
          onCancel: () => resolve(false),
          onClose: () => resolve(false),
        }),
      );
      return !leave;
    },
    enableBeforeUnload: () => draftsRef.current.size > 0 || busy.current > 0,
  });

  // Each change waits a moment for the next, then saves; an error waits for the person to try again.
  useEffect(() => {
    if (drafts.size === 0 || saving || saveError) return;
    const timer = setTimeout(() => void save(), SAVE_DELAY_MS);
    return () => clearTimeout(timer);
  }, [drafts, saving, saveError, save]);

  const scanned = async (code: string) => {
    if (!code) return;
    setScan("");
    setScanError(undefined);
    const keep = () => setScan((now) => now || code);
    try {
      const matches = await queryClient.fetchQuery(lookupQueryOptions(code));
      const location = matches.find((match) => match.kind === "location")?.location;
      if (location) {
        // The site's tree, waited for while it loads; beyond a truncated tree the server refuses the line instead.
        const tree = scopeIsSite
          ? undefined
          : await queryClient.ensureQueryData(siteLocationListQueryOptions(count.site.id, "active"));
        const inScope = !tree || tree.truncated || subtreeOf(tree.locations, count.scope.id).has(location.id);
        if (location.siteId !== count.site.id || !inScope) {
          keep();
          setScanError(t("scanOutOfScope", { code: location.fullCode }));
          return;
        }
        setCurrent(location);
        setOnlyCurrent(true);
        return;
      }
      const variant = matches.find((match) => match.kind === "variant")?.variant;
      if (!variant) {
        keep();
        setScanError(t("scanNothing", { code }));
        return;
      }
      if (!current) {
        keep();
        setScanError(t("scanLocationFirst"));
        return;
      }
      const key = keyOf(variant.id, current.id);
      const row = rows.find((one) => one.key === key) ?? { key, variant, location: current };
      if (!rows.some((one) => one.key === key)) setAdded((now) => [...now, row]);
      const packaging = variant.packagings?.find((one) => one.barcode === code);
      if (packaging)
        setDrafts((now) => {
          const draft = now.get(key) ?? savedDraft(row.line);
          return new Map(now).set(key, {
            ...draft,
            packs: { ...draft.packs, [packaging.id]: (draft.packs[packaging.id] ?? 0) + 1 },
          });
        });
      setFocusRequest({ key });
    } catch (error) {
      keep();
      setScanError(refusalMessage(t, error as Error));
    }
  };

  const posting = useMutation({
    mutationFn: async () => {
      // Waits for a save still out, then saves what is left, so no late answer lands after the post's.
      if (!(await save())) throw new NotSavedError(t("saveBeforePosting"));
      return postCount(count.id, { uncounted, ...(acceptMoved ? { acceptMoved: true } : {}) });
    },
    onSuccess: async (answer) => {
      queryClient.setQueryData(countQueryOptions(count.id).queryKey, answer);
      notifications.show({ color: "teal", title: t("countPosted"), message: t("countNumber", { id: count.id }) });
      await invalidate();
    },
    onError: async (error) => {
      setPostError(error instanceof NotSavedError ? error.message : refusalMessage(t, error));
      // The balances moved again since the screen read them: it reads them anew.
      if (refusalCode(error) === "count_lines_moved") {
        setAcceptMoved(false);
        await queryClient.invalidateQueries({ queryKey: countKey });
      }
      // Somebody else posted or cancelled it: the screen shows what it came to.
      if (refusalCode(error) === "count_not_open") await queryClient.invalidateQueries({ queryKey: countKey });
    },
  });

  const confirmPost = () =>
    modals.openConfirmModal({
      title: t("postCountTitle", { id: count.id }),
      children: <Text size="sm">{t("postCountBody")}</Text>,
      labels: { confirm: t("postCount"), cancel: t("cancel") },
      onConfirm: () => {
        setPostError(undefined);
        posting.mutate();
      },
    });

  const shown = useMemo(
    () => (current && onlyCurrent ? rows.filter((row) => row.location.id === current.id) : rows),
    [rows, current, onlyCurrent],
  );
  // Only the lines near the window are drawn: a whole site's count may hold thousands. The table scrolls with the page,
  // so the lines start where its body does.
  const [linesBody, setLinesBody] = useState<HTMLTableSectionElement | null>(null);
  const virtualizer = useWindowVirtualizer({
    count: shown.length,
    estimateSize: () => LINE_HEIGHT_ESTIMATE,
    overscan: LINES_OVERSCAN,
    scrollMargin: linesBody ? linesBody.getBoundingClientRect().top + window.scrollY : 0,
    getItemKey: (index) => shown[index]?.key ?? index,
    measureElement: (element) => element.getBoundingClientRect().height || LINE_HEIGHT_ESTIMATE,
  });
  const drawn = virtualizer.getVirtualItems();
  const margin = virtualizer.options.scrollMargin;
  const spaceAbove = drawn.length > 0 ? drawn[0].start - margin : 0;
  const spaceBelow = drawn.length > 0 ? virtualizer.getTotalSize() - (drawn[drawn.length - 1].end - margin) : 0;

  // A scanned item's line is scrolled to when it is not drawn, and its field takes the focus once it is.
  useEffect(() => {
    if (!focusRequest || focused.current === focusRequest) return;
    const field = document.getElementById(`count-line-${focusRequest.key}`) as HTMLInputElement | null;
    if (!field) {
      const index = shown.findIndex((row) => row.key === focusRequest.key);
      if (index >= 0) virtualizer.scrollToIndex(index, { align: "center" });
      return;
    }
    focused.current = focusRequest;
    field.focus();
    field.select();
    field.scrollIntoView({ block: "center" });
  }, [focusRequest, shown, drawn, virtualizer]);

  const unsaved = drafts.size > 0;
  const countedRows = rows.filter((row) => draftTotal(row.variant, draftOf(row)) !== "");
  const moved = lines.filter((line) => line.moved);
  // Only a line that posts is judged moved: a counted one, or every one when the uncounted are set to zero.
  const movedPosting = rows.flatMap((row) =>
    row.line?.moved && (uncounted === "zero" || draftTotal(row.variant, draftOf(row)) !== "") ? [row.line] : [],
  );
  const differences = rows
    .map((row) => {
      const total = draftTotal(row.variant, draftOf(row));
      const now = row.line?.current ?? 0;
      const counted = total === "" ? (uncounted === "zero" ? 0 : undefined) : total;
      return { row, now, counted, difference: counted === undefined ? 0 : roundQuantity(counted - now) };
    })
    .filter((one) => one.counted !== undefined && one.difference !== 0);

  return (
    <Stack gap="md">
      <Tabs value={tab} onChange={setTab}>
        <Tabs.List>
          <Tabs.Tab value="count">{t("countTab")}</Tabs.Tab>
          <Tabs.Tab value="review">{t("reviewTab")}</Tabs.Tab>
        </Tabs.List>
      </Tabs>
      <Group justify="space-between">
        <Text size="sm" c="dimmed">
          {t("countProgress", { counted: countedRows.length, lines: rows.length })}
        </Text>
        <Group gap="xs">
          <Text size="sm" c={saveError ? "red" : unsaved || saving ? "dimmed" : "teal"} aria-live="polite">
            {saveError ? t("notSaved") : saving ? t("saving") : unsaved ? t("unsavedChanges") : t("allSaved")}
          </Text>
          {(unsaved || saveError) && (
            <Button
              size="xs"
              variant="light"
              loading={saving}
              onClick={() => {
                setSaveError(undefined);
                void save();
              }}
            >
              {t("saveNow")}
            </Button>
          )}
        </Group>
      </Group>
      {saveError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {saveError}
        </Alert>
      )}
      {tab === "count" ? (
        <PageSection>
          <Stack gap="sm">
            <TextInput
              ref={scanRef}
              autoFocus
              label={t("countScanLabel")}
              description={t("countScanHint")}
              leftSection={<IconScan size={16} />}
              value={scan}
              error={scanError}
              onChange={(event) => {
                setScan(event.currentTarget.value);
                setScanError(undefined);
              }}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                void scanned(event.currentTarget.value.trim());
              }}
            />
            {current && (
              <Group justify="space-between">
                <Text size="sm">
                  {t("countingAt")}{" "}
                  <Text span ff="monospace" fw={600}>
                    {current.fullCode}
                  </Text>
                </Text>
                <Switch
                  label={t("onlyCurrentLocation")}
                  checked={onlyCurrent}
                  onChange={(event) => setOnlyCurrent(event.currentTarget.checked)}
                />
              </Group>
            )}
            {shown.length === 0 ? (
              <Text size="sm" c="dimmed">
                {current ? t("nothingExpectedHere") : t("countHasNoLines")}
              </Text>
            ) : (
              <Table.ScrollContainer minWidth={820}>
                <Table verticalSpacing="xs">
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("columnLocation")}</Table.Th>
                      <Table.Th>{t("columnSku")}</Table.Th>
                      <Table.Th>{t("columnProduct")}</Table.Th>
                      <Table.Th ta="right">{t("columnExpected")}</Table.Th>
                      <Table.Th ta="right">{t("columnCurrent")}</Table.Th>
                      <Table.Th>{t("columnCounted")}</Table.Th>
                      <Table.Th ta="right">{t("columnDifference")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody ref={setLinesBody}>
                    {spaceAbove > 0 && (
                      <tr>
                        <td colSpan={7} style={{ height: spaceAbove, padding: 0, border: 0 }} />
                      </tr>
                    )}
                    {drawn.map((item) => {
                      const row = shown[item.index];
                      const draft = draftOf(row);
                      const total = draftTotal(row.variant, draft);
                      const now = row.line?.current ?? 0;
                      const packagings = row.variant.packagings ?? [];
                      const label = `${row.location.fullCode} ${row.variant.sku}`;
                      const problem = marks.get(row.key);
                      return (
                        <Table.Tr
                          key={row.key}
                          ref={virtualizer.measureElement}
                          data-index={item.index}
                          data-moved={row.line?.moved ? "true" : undefined}
                          data-refused={problem ? "true" : undefined}
                          bg={
                            problem
                              ? "var(--mantine-color-red-light)"
                              : current?.id === row.location.id
                                ? "var(--mantine-color-blue-light)"
                                : undefined
                          }
                        >
                          <Table.Td>
                            <Text size="sm" ff="monospace">
                              {row.location.fullCode}
                            </Text>
                          </Table.Td>
                          <Table.Td>
                            <Text size="sm">{row.variant.sku}</Text>
                            <Group gap={4}>
                              {(row.line?.added || !row.line) && (
                                <Badge size="xs" variant="light" color="grape">
                                  {t("lineAdded")}
                                </Badge>
                              )}
                              {row.line?.moved && (
                                <Badge size="xs" variant="light" color="orange">
                                  {t("lineMoved")}
                                </Badge>
                              )}
                            </Group>
                            {problem && (
                              <Stack gap={0}>
                                <Text size="xs" c="red" fw={500}>
                                  {problem.message}
                                </Text>
                                {problem.detail && (
                                  <Text size="xs" c="dimmed">
                                    {problem.detail}
                                  </Text>
                                )}
                              </Stack>
                            )}
                          </Table.Td>
                          <Table.Td>{row.variant.productName}</Table.Td>
                          <Table.Td ta="right">
                            <Quantity value={row.line?.expected ?? 0} unit={row.variant.unit} packagings={packagings} />
                          </Table.Td>
                          <Table.Td ta="right">
                            <Quantity value={now} unit={row.variant.unit} packagings={packagings} />
                          </Table.Td>
                          <Table.Td w={packagings.length > 0 ? 200 : 150}>
                            <Stack gap={4}>
                              {packagings.length > 0 && (
                                <PackagingSteppers
                                  packagings={packagings}
                                  unit={row.variant.unit}
                                  counts={draft.packs}
                                  onChange={(packs) => edit(row, { packs })}
                                  label={(packaging) => t("countedPackagingOf", { name: packaging.name, line: label })}
                                />
                              )}
                              <NumberInput
                                id={`count-line-${row.key}`}
                                size={packagings.length > 0 ? "xs" : undefined}
                                aria-label={t(packagings.length > 0 ? "countedLooseOf" : "countedOf", { line: label })}
                                decimalScale={QUANTITY_DECIMALS}
                                allowNegative={false}
                                min={0}
                                rightSection={
                                  <Text size="xs" c="dimmed" pr="xs">
                                    {row.variant.unit}
                                  </Text>
                                }
                                rightSectionWidth={48}
                                value={draft.counted}
                                onChange={(value) =>
                                  edit(row, { counted: value === "" ? "" : roundQuantity(Number(value)) })
                                }
                              />
                              {isPackedDraft(draft) && total !== "" && (
                                <Text size="xs" c="dimmed">
                                  = {quantity(total, row.variant.unit)}
                                </Text>
                              )}
                            </Stack>
                          </Table.Td>
                          <Table.Td ta="right">
                            {total === "" ? (
                              <Text size="sm" c="dimmed">
                                —
                              </Text>
                            ) : (
                              <Text size="sm" c={total === now ? "dimmed" : total > now ? "teal" : "red"}>
                                {total > now ? "+" : ""}
                                {number(roundQuantity(total - now))}
                              </Text>
                            )}
                          </Table.Td>
                        </Table.Tr>
                      );
                    })}
                    {spaceBelow > 0 && (
                      <tr>
                        <td colSpan={7} style={{ height: spaceBelow, padding: 0, border: 0 }} />
                      </tr>
                    )}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
          </Stack>
        </PageSection>
      ) : (
        <Stack gap="md">
          <PageSection>
            <SimpleGrid cols={{ base: 2, sm: 4 }}>
              <Fact label={t("columnLines")}>{rows.length}</Fact>
              <Fact label={t("reviewCounted")}>{countedRows.length}</Fact>
              <Fact label={t("reviewUncounted")}>{rows.length - countedRows.length}</Fact>
              <Fact label={t("columnMoved")}>{moved.length}</Fact>
            </SimpleGrid>
          </PageSection>
          <PageSection>
            <Stack gap="sm">
              <Text fw={500}>{t("reviewDifferences")}</Text>
              {differences.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noDifferences")}
                </Text>
              ) : (
                <Table.ScrollContainer minWidth={560}>
                  <Table striped verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("columnLocation")}</Table.Th>
                        <Table.Th>{t("columnSku")}</Table.Th>
                        <Table.Th ta="right">{t("columnCurrent")}</Table.Th>
                        <Table.Th ta="right">{t("columnCounted")}</Table.Th>
                        <Table.Th ta="right">{t("columnDifference")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {differences.map(({ row, now, counted, difference }) => (
                        <Table.Tr key={row.key}>
                          <Table.Td>
                            <Text size="sm" ff="monospace">
                              {row.location.fullCode}
                            </Text>
                          </Table.Td>
                          <Table.Td>{row.variant.sku}</Table.Td>
                          <Table.Td ta="right">{quantity(now, row.variant.unit)}</Table.Td>
                          <Table.Td ta="right">{quantity(counted ?? 0, row.variant.unit)}</Table.Td>
                          <Table.Td ta="right" c={difference > 0 ? "teal" : "red"}>
                            {difference > 0 ? "+" : ""}
                            {quantity(difference, row.variant.unit)}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              )}
            </Stack>
          </PageSection>
          {movedPosting.length > 0 && (
            <PageSection>
              <Stack gap="sm">
                <Text fw={500}>{t("reviewMoved", { count: movedPosting.length })}</Text>
                <Text size="sm" c="dimmed">
                  {t("reviewMovedHint")}
                </Text>
                {movedPosting.map((line) => (
                  <Text key={line.id} size="sm">
                    <Text span ff="monospace">
                      {line.location.fullCode}
                    </Text>{" "}
                    {line.variant.sku}:{" "}
                    {t("movedFromTo", {
                      expected: quantity(line.expected, line.variant.unit),
                      current: quantity(line.current, line.variant.unit),
                    })}
                  </Text>
                ))}
                <Checkbox
                  label={t("acceptMoved")}
                  checked={acceptMoved}
                  onChange={(event) => setAcceptMoved(event.currentTarget.checked)}
                />
              </Stack>
            </PageSection>
          )}
          <PageSection>
            <Radio.Group
              label={t("uncountedLines")}
              description={t("uncountedHint")}
              value={uncounted}
              onChange={(value) => setUncounted(value as InventoryUncounted)}
            >
              <Stack gap="xs" mt="xs">
                <Radio value="keep" label={t("uncounted_keep")} />
                <Radio value="zero" label={t("uncounted_zero")} />
              </Stack>
            </Radio.Group>
          </PageSection>
          {postError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
              {postError}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button
              onClick={confirmPost}
              loading={posting.isPending}
              disabled={movedPosting.length > 0 && !acceptMoved}
            >
              {t("postCount")}
            </Button>
          </Group>
        </Stack>
      )}
    </Stack>
  );
};

/** A posted or cancelled count: who closed it and when, the movement a posted one wrote, and its lines. */
const ClosedCount = ({ count }: { count: InventoryCount }) => {
  const { t, stamp, quantity } = useInventoryFormat();
  return (
    <Stack gap="md">
      <PageSection>
        <SimpleGrid cols={{ base: 1, xs: 2, md: 4 }}>
          {count.postedAt && <Fact label={t("columnPostedAt")}>{stamp(count.postedAt)}</Fact>}
          {count.postedBy && <Fact label={t("columnPostedBy")}>{count.postedBy.displayName}</Fact>}
          {count.cancelledAt && <Fact label={t("cancelledAt")}>{stamp(count.cancelledAt)}</Fact>}
          {count.cancelledBy && <Fact label={t("cancelledBy")}>{count.cancelledBy.displayName}</Fact>}
          {count.uncounted && <Fact label={t("uncountedLines")}>{t(`uncounted_${count.uncounted}`)}</Fact>}
          {count.movementId !== undefined && (
            <Fact label={t("columnMovement")}>
              <Anchor
                size="sm"
                renderRoot={(props) => <Link {...movementLinkOptions(count.movementId ?? 0)} {...props} />}
              >
                {t("openPosted", { id: count.movementId })}
              </Anchor>
            </Fact>
          )}
        </SimpleGrid>
      </PageSection>
      <PageSection>
        <Table.ScrollContainer minWidth={560}>
          <Table striped verticalSpacing="xs">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("columnLocation")}</Table.Th>
                <Table.Th>{t("columnSku")}</Table.Th>
                <Table.Th ta="right">{t("columnExpected")}</Table.Th>
                <Table.Th ta="right">{t("columnCounted")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {(count.lines ?? []).map((line) => (
                <Table.Tr key={line.id}>
                  <Table.Td>
                    <Text size="sm" ff="monospace">
                      {line.location.fullCode}
                    </Text>
                  </Table.Td>
                  <Table.Td>{line.variant.sku}</Table.Td>
                  <Table.Td ta="right">{quantity(line.expected, line.variant.unit)}</Table.Td>
                  <Table.Td ta="right">
                    {line.counted === undefined ? (
                      <Text size="sm" c="dimmed">
                        —
                      </Text>
                    ) : (
                      <Quantity value={line.counted} unit={line.variant.unit} packagings={line.variant.packagings} />
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </PageSection>
    </Stack>
  );
};

/**
 * One count (I16–I19): while counting, the counting screen and its review,
 * and Cancel count; once posted or cancelled, what it came to. Counts need
 * `inventory:counts-manage` and both views (design §5).
 */
export const CountPage = () => {
  const { t, stamp } = useInventoryFormat();
  const { countId: rawId } = useParams({ strict: false }) as { countId: string };
  const countId = Number(rawId);
  const queryClient = useQueryClient();
  const invalidate = useInvalidateInventory();
  const meta = useQuery(inventoryMetaQueryOptions());
  const allowed = canCount(meta.data);
  const count = useQuery({ ...countQueryOptions(countId), enabled: allowed });
  const data = count.data;
  // The counting screen's last save: Cancel waits for it, so its answer cannot land after the cancel's.
  const savesRef = useRef<Promise<boolean>>(Promise.resolve(true));

  const cancelling = useMutation({
    mutationFn: async () => {
      await savesRef.current;
      return cancelCount(countId);
    },
    onSuccess: async (answer) => {
      queryClient.setQueryData(countQueryOptions(countId).queryKey, answer);
      notifications.show({ color: "teal", title: t("countCancelled"), message: t("countNumber", { id: countId }) });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotCancel"), message: refusalMessage(t, error) }),
  });

  const confirmCancel = () =>
    modals.openConfirmModal({
      title: t("cancelCountTitle", { id: countId }),
      children: <Text size="sm">{t("cancelCountBody")}</Text>,
      labels: { confirm: t("cancelCount"), cancel: t("keepCounting") },
      confirmProps: { color: "red" },
      onConfirm: () => cancelling.mutate(),
    });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: t("navCounts"), to: COUNTS_ROUTE_PATH },
          { label: t("countNumber", { id: countId }) },
        ]}
        title={
          <Group gap="sm">
            <span>{t("countNumber", { id: countId })}</span>
            {data && (
              <Badge variant="light" color={countStatusColor[data.status]}>
                {t(`countStatus_${data.status}`)}
              </Badge>
            )}
          </Group>
        }
        description={
          data &&
          [
            `${data.site.code} · ${data.site.name}`,
            data.scope.kind === "site" ? t("countWholeSite") : data.scope.fullCode,
            t("openedByAt", { who: data.openedBy.displayName, when: stamp(data.openedAt) }),
            data.note,
          ]
            .filter(Boolean)
            .join(" — ")
        }
        actions={
          data?.status === "counting" && (
            <Button
              variant="default"
              color="red"
              leftSection={<IconX size={16} />}
              onClick={confirmCancel}
              loading={cancelling.isPending}
            >
              {t("cancelCount")}
            </Button>
          )
        }
      />
      {meta.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {meta.error.message}
        </Alert>
      ) : meta.isPending ? (
        <ContentSkeleton rows={6} />
      ) : !allowed ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_count")}
        </Alert>
      ) : count.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {count.error.message}
        </Alert>
      ) : !data ? (
        <ContentSkeleton rows={6} />
      ) : data.status === "counting" ? (
        <CountingScreen count={data} savesRef={savesRef} />
      ) : (
        <>
          {data.status === "posted" && (
            <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("countPosted")}>
              {data.movementId !== undefined && (
                <Anchor renderRoot={(props) => <Link {...movementLinkOptions(data.movementId ?? 0)} {...props} />}>
                  {t("openPosted", { id: data.movementId })}
                </Anchor>
              )}
            </Alert>
          )}
          <ClosedCount count={data} />
        </>
      )}
    </Stack>
  );
};

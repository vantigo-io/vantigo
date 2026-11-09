import { Alert, Badge, Button, Group, Select, SimpleGrid, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { type Dispatch, type SetStateAction, useRef, useState } from "react";
import { refusalCodes } from "../api/movements";
import { isRevisionConflict, refusalCode, refusedLines } from "../api/request";
import type { InventorySite } from "../api/sites";
import { createTransfer, type InventoryTransfer, type InventoryTransferInput, putTransfer } from "../api/transfers";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { type EditorLine, editorLinesOf, lineGap, lineQuantity, transferLinesBody } from "../lib/lines";
import { stockAtKey, useStockAtSites } from "../lib/stock-at-sites";
import { askToLeave, useTransferActions } from "../lib/transfer-actions";
import { LineEditor } from "./line-editor";
import { SendTransferDialog } from "./send-transfer-dialog";

export interface TransferFormProps {
  /** The active sites to choose among. */
  sites: readonly InventorySite[];
  /** The draft being edited; a new one when absent. */
  transfer?: InventoryTransfer;
  /** What a new draft opens with. */
  fromSiteId?: number;
  toSiteId?: number;
  /** The draft was saved, or the send dialog closed — sent or not — with the transfer as it is now. */
  onFinished: (transfer: InventoryTransfer) => void;
  /** The draft was deleted. */
  onDeleted?: () => void;
  /**
   * The person stopped editing, or the draft moved on under them — changed by
   * someone else, or sent — so the form closes on the transfer as it is now.
   */
  onCancel?: () => void;
}

type LineMarks = ReadonlyMap<string, { message: string; detail?: string }>;
type Then = "send" | "sendAndReceive";

const optional = (text: string) => (text.trim() ? text.trim() : undefined);

/**
 * A transfer's draft (I36, I37; phase 2 design §7): the from and the to site,
 * the expected arrival, a reference and a note, and the shared line editor
 * with two read-only columns from the stock at both sites (I53) — at the from
 * site what is available, at the to site what is available, what is on its
 * way and the reorder point when there is one. A line asking for more than
 * the source has is warned of here and refused only at send. Save draft keeps
 * it; Send… and Send and receive… save what changed first and open the send
 * dialog. A draft holds nothing, so Delete draft is a plain delete.
 */
export const TransferForm = ({
  sites,
  transfer,
  fromSiteId: from0,
  toSiteId: to0,
  onFinished,
  onDeleted,
  onCancel,
}: TransferFormProps) => {
  const { t, quantity } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const { deleteDraft } = useTransferActions();
  const [saved, setSaved] = useState<InventoryTransfer | undefined>(transfer);
  const [dirty, setDirty] = useState(!transfer);
  const [fromSiteId, setFromSiteId] = useState<number | undefined>(transfer?.fromSite.id ?? from0);
  const [toSiteId, setToSiteId] = useState<number | undefined>(transfer?.toSite.id ?? to0);
  const [expectedArrival, setExpectedArrival] = useState<string | null>(transfer?.expectedArrival ?? null);
  const [reference, setReference] = useState(transfer?.reference ?? "");
  const [note, setNote] = useState(transfer?.note ?? "");
  const [lines, setLinesState] = useState<EditorLine[]>(() => (transfer ? editorLinesOf(transfer.lines) : []));
  const [marks, setMarks] = useState<LineMarks>(new Map());
  const [problem, setProblem] = useState<string>();
  const [headerErrors, setHeaderErrors] = useState<{ from?: string; to?: string }>({});
  const [sending, setSending] = useState<Then>();
  // Whether the person changed something not yet saved; a ref, so a navigation right after a save is not stopped.
  const touched = useRef(false);

  const changed =
    <T,>(set: (value: T) => void) =>
    (value: T) => {
      set(value);
      setDirty(true);
      touched.current = true;
    };
  const setLines: Dispatch<SetStateAction<EditorLine[]>> = (next) => {
    setLinesState(next);
    setDirty(true);
    touched.current = true;
  };

  const leaveWords = {
    title: t("leaveDraftTitle"),
    body: t("leaveDraftBody"),
    leave: t("leaveDraft"),
    stay: t("keepEditing"),
  };
  // Leaving with unsaved changes asks first, and closing the tab warns.
  useBlocker({
    shouldBlockFn: async () => (touched.current ? !(await askToLeave(leaveWords)) : false),
    enableBeforeUnload: () => touched.current,
  });
  const cancel = async () => {
    if (touched.current && !(await askToLeave(leaveWords))) return;
    touched.current = false;
    onCancel?.();
  };

  const fromSite =
    sites.find((site) => site.id === fromSiteId) ??
    (transfer && fromSiteId === transfer.fromSite.id ? transfer.fromSite : undefined);
  const toSite =
    sites.find((site) => site.id === toSiteId) ??
    (transfer && toSiteId === transfer.toSite.id ? transfer.toSite : undefined);
  const stock = useStockAtSites(
    lines.flatMap((line) => (line.variant && !line.variant.deleted ? [line.variant.id] : [])),
    [fromSiteId, toSiteId],
  );
  // What the draft asks of each variant, every line of it together: a line is short when they ask more than the source has.
  // Summed in thousandths, as quantities are kept (I11): 0.1 + 0.2 is not more than 0.3.
  const asked = new Map<number, number>();
  for (const line of lines) {
    const n = lineQuantity("transfer", line);
    if (line.variant && n !== "") asked.set(line.variant.id, (asked.get(line.variant.id) ?? 0) + Math.round(n * 1000));
  }

  const saving = useMutation({
    mutationFn: ({ body }: { body: InventoryTransferInput; then?: Then }) =>
      saved ? putTransfer(saved.id, body, saved.revision) : createTransfer(body),
    onSuccess: async (answer, { then }) => {
      setSaved(answer);
      setDirty(false);
      touched.current = false;
      setMarks(new Map());
      setProblem(undefined);
      await invalidate();
      if (then) setSending(then);
      else {
        notifications.show({
          color: "teal",
          title: t("draftSaved"),
          message: t("transferNumber", { number: answer.number }),
        });
        onFinished(answer);
      }
    },
    onError: async (error, { body }) => {
      const moved = isRevisionConflict(error) || refusalCode(error) === "transfer_not_draft";
      if (moved) {
        await invalidate();
        // The draft being edited moved on: the form closes on the transfer as it is now, and says why.
        if (transfer && onCancel) {
          notifications.show({
            color: "yellow",
            title: t("transferChanged"),
            message: isRevisionConflict(error) ? t("transferChangedHint") : t("refusal.transfer_not_draft"),
          });
          touched.current = false;
          onCancel();
          return;
        }
        setProblem(isRevisionConflict(error) ? t("revisionConflictHint") : t("refusal.transfer_not_draft"));
        return;
      }
      const failing = refusedLines(error);
      const next = new Map<string, { message: string; detail?: string }>();
      for (const line of failing) {
        const row = lines[line.index];
        if (!row || line.index >= body.lines.length) continue;
        next.set(
          row.key,
          (refusalCodes as readonly string[]).includes(line.code)
            ? { message: t(`refusal.${line.code}`), detail: line.detail }
            : { message: line.detail },
        );
      }
      setMarks(next);
      setProblem(failing.length > 0 ? t("draftLinesRefused", { count: failing.length }) : refusalMessage(t, error));
      if (refusalCode(error) === "transfer_same_site") setHeaderErrors({ to: t("refusal.transfer_same_site") });
    },
  });

  const submit = (then?: Then) => {
    const errors = {
      from: fromSiteId === undefined ? t("required") : undefined,
      to:
        toSiteId === undefined ? t("required") : toSiteId === fromSiteId ? t("refusal.transfer_same_site") : undefined,
    };
    setHeaderErrors(errors);
    const gaps = new Map<string, { message: string }>();
    for (const line of lines) {
      const gap = lineGap("transfer", line);
      if (gap) gaps.set(line.key, { message: t(`lineGap_${gap}`) });
    }
    setMarks(gaps);
    if (lines.length === 0) {
      setProblem(t("noLines"));
      return;
    }
    if (errors.from || errors.to || gaps.size > 0 || fromSiteId === undefined || toSiteId === undefined) {
      setProblem(gaps.size > 0 ? t("linesIncomplete") : undefined);
      return;
    }
    setProblem(undefined);
    // Nothing changed since the last save: the send goes on the draft as it is.
    if (then && saved && !dirty) {
      setSending(then);
      return;
    }
    saving.mutate({
      body: {
        fromSiteId,
        toSiteId,
        ...(expectedArrival ? { expectedArrival } : {}),
        reference: optional(reference),
        note: optional(note),
        lines: transferLinesBody(lines),
      },
      then,
    });
  };

  const siteOptions = sites.map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }));

  return (
    <Stack gap="lg">
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <Select
          label={t("fromSite")}
          withAsterisk
          data={siteOptions}
          value={fromSiteId === undefined ? null : String(fromSiteId)}
          onChange={(next) => changed(setFromSiteId)(next === null ? undefined : Number(next))}
          error={headerErrors.from}
        />
        <Select
          label={t("toSite")}
          withAsterisk
          data={siteOptions}
          value={toSiteId === undefined ? null : String(toSiteId)}
          onChange={(next) => changed(setToSiteId)(next === null ? undefined : Number(next))}
          error={headerErrors.to}
        />
        <DatePickerInput
          label={t("expectedArrival")}
          clearable
          valueFormat={t("dateInputFormat")}
          value={expectedArrival}
          onChange={(next) => changed(setExpectedArrival)(next)}
        />
        <TextInput
          label={t("transferReference")}
          maxLength={100}
          value={reference}
          onChange={(event) => changed(setReference)(event.currentTarget.value)}
        />
      </SimpleGrid>
      <Textarea
        label={t("note")}
        maxLength={2000}
        minRows={2}
        value={note}
        onChange={(event) => changed(setNote)(event.currentTarget.value)}
      />
      <LineEditor
        kind="transfer"
        siteId={undefined}
        lines={lines}
        onChange={setLines}
        defaultLocationId={null}
        lineErrors={marks}
        extraColumns={[
          {
            key: "from",
            header: t("atSiteAvailable", { site: fromSite?.code ?? t("fromSite") }),
            cell: (line) => {
              if (!line.variant || fromSiteId === undefined) return null;
              const here = stock.data?.get(stockAtKey(line.variant.id, fromSiteId));
              if (!here)
                return (
                  <Text size="sm" c="dimmed">
                    …
                  </Text>
                );
              const short = (asked.get(line.variant.id) ?? 0) > Math.round(here.available * 1000);
              return (
                <Group gap={4} wrap="nowrap">
                  <Text size="sm">{quantity(here.available, line.variant.unit)}</Text>
                  {short && (
                    <Badge size="xs" variant="light" color="orange" title={t("shortHint")}>
                      {t("short")}
                    </Badge>
                  )}
                </Group>
              );
            },
          },
          {
            key: "to",
            header: t("atSiteAvailableIncoming", { site: toSite?.code ?? t("toSite") }),
            cell: (line) => {
              if (!line.variant || toSiteId === undefined) return null;
              const there = stock.data?.get(stockAtKey(line.variant.id, toSiteId));
              if (!there)
                return (
                  <Text size="sm" c="dimmed">
                    …
                  </Text>
                );
              const reorderPoint = there.placement?.reorderPoint;
              return (
                <Group gap={4} wrap="nowrap">
                  <Text size="sm">
                    {[
                      quantity(there.available, line.variant.unit),
                      ...(there.incoming > 0 ? [t("incomingQuantity", { quantity: quantity(there.incoming) })] : []),
                      ...(reorderPoint !== null && reorderPoint !== undefined
                        ? [t("reorderAt", { quantity: quantity(reorderPoint) })]
                        : []),
                    ].join(" · ")}
                  </Text>
                  {there.placement?.low && (
                    <Badge size="xs" variant="light" color="red">
                      {t("low")}
                    </Badge>
                  )}
                </Group>
              );
            },
          },
        ]}
      />
      {stock.error && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("stockAtSitesUnavailable")}
        </Alert>
      )}
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="space-between">
        <Group gap="xs">
          <Button onClick={() => submit()} loading={saving.isPending && !sending} variant="light">
            {t("saveDraft")}
          </Button>
          <Button onClick={() => submit("send")} loading={saving.isPending}>
            {t("sendEllipsis")}
          </Button>
          <Button onClick={() => submit("sendAndReceive")} loading={saving.isPending} variant="light">
            {t("sendAndReceiveEllipsis")}
          </Button>
        </Group>
        <Group gap="xs">
          {onCancel && (
            <Button variant="default" onClick={() => void cancel()}>
              {t("cancel")}
            </Button>
          )}
          {saved && onDeleted && (
            <Button
              color="red"
              variant="subtle"
              onClick={() =>
                deleteDraft(saved, () => {
                  touched.current = false;
                  onDeleted();
                })
              }
            >
              {t("deleteDraft")}
            </Button>
          )}
        </Group>
      </Group>
      {sending && saved && (
        <SendTransferDialog
          opened
          transfer={saved}
          withReceive={sending === "sendAndReceive"}
          onClose={(sent) => {
            setSending(undefined);
            onFinished(sent ?? saved);
          }}
        />
      )}
    </Stack>
  );
};

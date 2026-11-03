import {
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import {
  adjustmentReasons,
  type InventoryAdjustmentReason,
  type InventoryMovement,
  postAdjustment,
  postMove,
  postReceipt,
  refusalCodes,
} from "../api/movements";
import { refusalCode, refusedLines } from "../api/request";
import type { InventorySite } from "../api/sites";
import type { InventoryVariantRef } from "../api/stock";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import {
  adjustmentBody,
  type EditorLine,
  type LineEditorKind,
  lineGap,
  moveBody,
  newEditorLine,
  type PostingHeader,
  receiptBody,
} from "../lib/lines";
import { movementLinkOptions, movementsLinkOptions } from "../lib/routes";
import { LineEditor } from "./line-editor";
import { LocationPicker } from "./location-picker";

export interface MovementFormProps {
  kind: LineEditorKind;
  /** The active sites to choose among. */
  sites: readonly InventorySite[];
  /** The site the form opens on: asked for, the location's, or the only one. */
  siteId?: number;
  /** Where the form was opened from: Adjust's and Move from here's location, Receive here's default. */
  locationId?: number;
  /** A variant to open with one row of, as the variant's stock page asks. */
  variant?: InventoryVariantRef;
}

type LineMarks = ReadonlyMap<string, { message: string; detail?: string }>;

/** Whether a failed posting was answered with a refusal, so nothing was stored: a 4xx, not a network error or a 5xx. */
const definitelyRefused = (error: Error) => {
  const status = (error as { status?: unknown }).status;
  return typeof status === "number" && status >= 400 && status < 500;
};

/**
 * An adjustment, a move or a receipt (I15): the header the kind asks for, and
 * the shared line editor. The `clientRequestId` (I14) is made when the form
 * mounts. A failure that may have been stored — no answer, or a 5xx — keeps
 * it, so a retry is recognised as the same posting; a refusal stored nothing
 * and the next try is another key. When the server says the key was already
 * used for other content (`client_request_reused`), an earlier version of
 * this posting landed and its answer was lost: the form says so, links to the
 * site's movements to check, and offers to post this as a new posting. A
 * success empties the form for the next one, with a link to what was posted.
 */
export const MovementForm = ({ kind, sites, siteId: initialSite, locationId, variant }: MovementFormProps) => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  // Where the form was opened from; a new site empties it, as it does the picks.
  const [defaultLocation, setDefaultLocation] = useState<number | null>(locationId ?? null);
  const [siteId, setSiteId] = useState<number | undefined>(initialSite);
  const [lines, setLines] = useState<EditorLine[]>(() =>
    variant ? [newEditorLine(kind, defaultLocation, variant)] : [],
  );
  const [reason, setReason] = useState<InventoryAdjustmentReason | null>(null);
  const [note, setNote] = useState("");
  const [toLocationId, setToLocationId] = useState<number | null>(kind === "receipt" ? defaultLocation : null);
  const [supplierName, setSupplierName] = useState("");
  const [supplierReference, setSupplierReference] = useState("");
  const [clientRequestId, setClientRequestId] = useState(() => crypto.randomUUID());
  const [marks, setMarks] = useState<LineMarks>(new Map());
  const [problem, setProblem] = useState<string>();
  const [reused, setReused] = useState(false);
  const [headerErrors, setHeaderErrors] = useState<{ site?: string; reason?: string; toLocation?: string }>({});
  const [posted, setPosted] = useState<InventoryMovement>();

  const posting = useMutation({
    mutationFn: ({ header, sent }: { header: PostingHeader; sent: readonly EditorLine[] }) =>
      kind === "adjustment"
        ? postAdjustment(adjustmentBody(header, sent))
        : kind === "move"
          ? postMove(moveBody(header, sent))
          : postReceipt(receiptBody(header, sent)),
    onSuccess: async (movement) => {
      setPosted(movement);
      setLines([]);
      setNote("");
      setSupplierName("");
      setSupplierReference("");
      setMarks(new Map());
      setProblem(undefined);
      setReused(false);
      // The key belongs to the posting that landed; the next one is another.
      setClientRequestId(crypto.randomUUID());
      notifications.show({
        color: "teal",
        title: t(`posted_${kind}`),
        message: t("postedNumber", { id: movement.id }),
      });
      await invalidate();
    },
    onError: (error, { sent }) => {
      if (refusalCode(error) === "client_request_reused") {
        setMarks(new Map());
        setProblem(undefined);
        setReused(true);
        return;
      }
      // Nothing was stored: the next try is another posting.
      if (definitelyRefused(error)) setClientRequestId(crypto.randomUUID());
      const failing = refusedLines(error);
      const next = new Map<string, { message: string; detail?: string }>();
      for (const line of failing) {
        const row = sent[line.index];
        if (!row) continue;
        next.set(
          row.key,
          (refusalCodes as readonly string[]).includes(line.code)
            ? { message: t(`refusal.${line.code}`), detail: line.detail }
            : { message: line.detail },
        );
      }
      setMarks(next);
      setProblem(failing.length > 0 ? t("linesRefused", { count: failing.length }) : refusalMessage(t, error));
    },
  });

  const submit = (key = clientRequestId) => {
    setPosted(undefined);
    setReused(false);
    const errors = {
      site: siteId === undefined ? t("required") : undefined,
      reason: kind === "adjustment" && reason === null ? t("required") : undefined,
      toLocation: kind === "receipt" && toLocationId === null ? t("required") : undefined,
    };
    setHeaderErrors(errors);
    const gaps = new Map<string, { message: string }>();
    for (const line of lines) {
      const gap = lineGap(kind, line);
      if (gap) gaps.set(line.key, { message: t(`lineGap_${gap}`) });
    }
    setMarks(gaps);
    if (lines.length === 0) {
      setProblem(t("noLines"));
      return;
    }
    if (errors.site || errors.reason || errors.toLocation || gaps.size > 0 || siteId === undefined) {
      setProblem(gaps.size > 0 ? t("linesIncomplete") : undefined);
      return;
    }
    setProblem(undefined);
    posting.mutate({
      header: { clientRequestId: key, siteId, note, reason, toLocationId, supplierName, supplierReference },
      sent: lines,
    });
  };

  const changeSite = (next: number | undefined) => {
    setSiteId(next);
    // Every location belongs to one site (I13): a new site empties the picks, and the default new rows take.
    setToLocationId(null);
    setDefaultLocation(null);
    setLines((current) => current.map((line) => ({ ...line, locationId: null, toLocationId: null })));
  };

  /** The person checked, or meant a second posting: this one goes as a new posting, under a new key. */
  const postAsNew = () => {
    const key = crypto.randomUUID();
    setClientRequestId(key);
    submit(key);
  };

  return (
    <Stack gap="lg">
      {posted && (
        <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t(`posted_${kind}`)}>
          <Anchor renderRoot={(props) => <Link {...movementLinkOptions(posted.id)} {...props} />}>
            {t("openPosted", { id: posted.id })}
          </Anchor>
        </Alert>
      )}
      <Card withBorder>
        <Stack gap="md">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Select
              label={t("site")}
              withAsterisk
              data={sites.map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
              value={siteId === undefined ? null : String(siteId)}
              onChange={(next) => changeSite(next === null ? undefined : Number(next))}
              error={headerErrors.site}
            />
            {kind === "adjustment" && (
              <Select
                label={t("reason")}
                withAsterisk
                data={adjustmentReasons.map((value) => ({ value, label: t(`reason_${value}`) }))}
                value={reason}
                onChange={(next) => setReason(next as InventoryAdjustmentReason | null)}
                error={headerErrors.reason}
              />
            )}
            {kind === "receipt" && (
              <LocationPicker
                siteId={siteId}
                label={t("receiptLocation")}
                required
                value={toLocationId}
                onChange={setToLocationId}
                error={headerErrors.toLocation}
              />
            )}
            {kind === "receipt" && (
              <TextInput
                label={t("supplierName")}
                maxLength={200}
                value={supplierName}
                onChange={(event) => setSupplierName(event.currentTarget.value)}
              />
            )}
            {kind === "receipt" && (
              <TextInput
                label={t("supplierReference")}
                maxLength={100}
                value={supplierReference}
                onChange={(event) => setSupplierReference(event.currentTarget.value)}
              />
            )}
          </SimpleGrid>
          <Textarea
            label={t("note")}
            maxLength={1000}
            minRows={2}
            value={note}
            onChange={(event) => setNote(event.currentTarget.value)}
          />
        </Stack>
      </Card>
      <Card withBorder>
        <LineEditor
          kind={kind}
          siteId={siteId}
          lines={lines}
          onChange={setLines}
          defaultLocationId={kind === "receipt" ? null : defaultLocation}
          lineErrors={marks}
        />
      </Card>
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotPost")}>
          {problem}
        </Alert>
      )}
      {reused && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("reusedTitle")}>
          <Stack gap="xs">
            <Text size="sm">{t("refusal.client_request_reused")}</Text>
            <Anchor
              size="sm"
              renderRoot={(props) => (
                <Link {...movementsLinkOptions(siteId === undefined ? {} : { siteId })} {...props} />
              )}
            >
              {t("reusedCheck")}
            </Anchor>
            <Text size="sm">{t("reusedHint")}</Text>
            <Group>
              <Button size="xs" variant="light" onClick={postAsNew} loading={posting.isPending}>
                {t("postAsNew")}
              </Button>
            </Group>
          </Stack>
        </Alert>
      )}
      <Group justify="flex-end">
        <Button onClick={() => submit()} loading={posting.isPending}>
          {t(`post_${kind}`)}
        </Button>
      </Group>
    </Stack>
  );
};

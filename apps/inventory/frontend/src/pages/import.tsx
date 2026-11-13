import {
  Alert,
  Anchor,
  Badge,
  Button,
  FileInput,
  Group,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
} from "@mantine/core";
import { IconAlertCircle, IconCircleCheck, IconDownload, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { IMPORT_HEADER, type InventoryImportResult, importOpeningBalances } from "../api/imports";
import { downloadBlob } from "../api/labels";
import { inventoryMetaQueryOptions } from "../api/meta";
import { type InventoryPlacementImportResult, importPlacements, PLACEMENTS_IMPORT_HEADER } from "../api/placements";
import { sitesQueryOptions } from "../api/sites";
import { canImport } from "../lib/capabilities";
import { refusalMessage, useInvalidateInventory, useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, movementLinkOptions } from "../lib/routes";

/** A file's bytes as they are, through FileReader where `Blob.arrayBuffer` is missing. */
const readBytes = (file: File): Promise<ArrayBuffer> =>
  typeof file.arrayBuffer === "function"
    ? file.arrayBuffer()
    : new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(file);
      });

/**
 * The import page: opening balances (I32) or placements (I61), chosen at the
 * top. The opening-balance import (I32): a site and a CSV of `location` (full
 * code), `sku` and `quantity`. A dry run answers each row's outcome; Commit,
 * offered only when every row is ok, posts one adjustment, reason Opening
 * balance, of every row — the same file twice is one movement. The file may
 * carry a tracked item's lot and its dates (manufacturing phase 2 M71), which
 * the preview shows. A template is
 * offered to start from, for either kind. It needs `inventory:stock-adjust`
 * and both views.
 */
type ImportKind = "openingBalances" | "placements";

export const ImportPage = () => {
  const { t } = useInventoryFormat();
  const meta = useQuery(inventoryMetaQueryOptions());
  const allowed = canImport(meta.data);
  const [kind, setKind] = useState<ImportKind>("openingBalances");

  const template = () =>
    kind === "placements"
      ? downloadBlob(new Blob([`${PLACEMENTS_IMPORT_HEADER}\n`], { type: "text/csv;charset=utf-8" }), "placements.csv")
      : downloadBlob(new Blob([`${IMPORT_HEADER}\n`], { type: "text/csv;charset=utf-8" }), "opening-balances.csv");

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("importTitle") }]}
        title={t("importTitle")}
        description={t("importDescription")}
        actions={
          allowed && (
            <Button variant="default" leftSection={<IconDownload size={16} />} onClick={template}>
              {t("importTemplate")}
            </Button>
          )
        }
      />
      {meta.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {meta.error.message}
        </Alert>
      ) : meta.isPending ? (
        <ContentSkeleton rows={3} />
      ) : !allowed ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_import")}
        </Alert>
      ) : (
        <>
          <SegmentedControl
            aria-label={t("importKind")}
            value={kind}
            onChange={(next) => setKind(next as ImportKind)}
            data={[
              { value: "openingBalances", label: t("importKind_openingBalances") },
              { value: "placements", label: t("importKind_placements") },
            ]}
          />
          {kind === "placements" ? <PlacementsImport /> : <OpeningBalancesImport />}
        </>
      )}
    </Stack>
  );
};

/** The opening balances of one site (I32): the site, the file, the dry run and the commit. */
const OpeningBalancesImport = () => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const sites = useQuery(sitesQueryOptions());
  const [siteId, setSiteId] = useState<number>();
  const [file, setFile] = useState<File | null>(null);
  const [checked, setChecked] = useState<{ result: InventoryImportResult; csv: ArrayBuffer; siteId: number }>();
  const [committed, setCommitted] = useState<InventoryImportResult>();
  const site = siteId ?? (sites.data?.length === 1 ? sites.data[0].id : undefined);

  const dryRun = useMutation({
    mutationFn: async ({ chosen, upload }: { chosen: number; upload: File }) => {
      const csv = await readBytes(upload);
      return { result: await importOpeningBalances(chosen, csv, true), csv, siteId: chosen };
    },
    onSuccess: (answer) => {
      setChecked(answer);
      setCommitted(undefined);
    },
  });

  const commit = useMutation({
    mutationFn: ({ csv, siteId: chosen }: { csv: ArrayBuffer; siteId: number }) =>
      importOpeningBalances(chosen, csv, false),
    onSuccess: async (result) => {
      if (result.movementId === undefined) {
        // Refused for its rows: nothing was posted, and the rows say why.
        setChecked((now) => now && { ...now, result });
        return;
      }
      setCommitted(result);
      setChecked(undefined);
      setFile(null);
      await invalidate();
    },
  });

  const reset = () => {
    setChecked(undefined);
    setCommitted(undefined);
    dryRun.reset();
    commit.reset();
  };

  const rows = checked?.result.rows ?? [];
  const failing = rows.filter((row) => row.outcome !== "ok").length;
  // The file names lots: the preview shows them.
  const withLots = rows.some((row) => row.lot !== undefined && row.lot !== "");

  return (
    <>
      {sites.isPending ? (
        <ContentSkeleton rows={3} />
      ) : (
        <>
          {committed?.movementId !== undefined && (
            <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("importPosted")}>
              <Anchor renderRoot={(props) => <Link {...movementLinkOptions(committed.movementId ?? 0)} {...props} />}>
                {t("openPosted", { id: committed.movementId })}
              </Anchor>
            </Alert>
          )}
          <PageSection>
            <Stack gap="md">
              <Text size="sm" c="dimmed">
                {t("importHint")}
              </Text>
              <Select
                label={t("site")}
                withAsterisk
                data={(sites.data ?? []).map((one) => ({ value: String(one.id), label: `${one.code} · ${one.name}` }))}
                value={site === undefined ? null : String(site)}
                onChange={(next) => {
                  setSiteId(next === null ? undefined : Number(next));
                  reset();
                }}
              />
              <FileInput
                data-assist="inventory.import.upload"
                label={t("importFile")}
                placeholder={t("importFilePlaceholder")}
                accept=".csv,text/csv"
                clearable
                leftSection={<IconFileUpload size={16} />}
                value={file}
                onChange={(next) => {
                  setFile(next);
                  reset();
                }}
              />
              <Group justify="flex-end">
                <Button
                  variant="light"
                  disabled={site === undefined || !file}
                  loading={dryRun.isPending}
                  onClick={() => {
                    if (site === undefined || !file) return;
                    // A new dry run answers anew: an earlier commit's refusal no longer applies.
                    commit.reset();
                    dryRun.mutate({ chosen: site, upload: file });
                  }}
                >
                  {t("importDryRun")}
                </Button>
              </Group>
            </Stack>
          </PageSection>
          {(dryRun.error || commit.error) && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importFailed")}>
              {refusalMessage(t, (dryRun.error ?? commit.error) as Error)}
            </Alert>
          )}
          {checked && (
            <PageSection>
              <Stack gap="md">
                <Group justify="space-between">
                  <Text fw={500}>
                    {checked.result.ok
                      ? t("importAllOk", { count: rows.length })
                      : t("importRowsFailing", { count: failing })}
                  </Text>
                  <Button
                    onClick={() => commit.mutate({ csv: checked.csv, siteId: checked.siteId })}
                    disabled={!checked.result.ok}
                    loading={commit.isPending}
                  >
                    {t("importCommit")}
                  </Button>
                </Group>
                <Table.ScrollContainer minWidth={560}>
                  <Table striped verticalSpacing="xs">
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("importRow")}</Table.Th>
                        <Table.Th>{t("columnLocation")}</Table.Th>
                        <Table.Th>{t("columnSku")}</Table.Th>
                        {withLots && <Table.Th>{t("columnLot")}</Table.Th>}
                        <Table.Th ta="right">{t("columnQuantity")}</Table.Th>
                        <Table.Th>{t("importOutcome")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {rows.map((row) => (
                        <Table.Tr key={row.row} data-outcome={row.outcome}>
                          <Table.Td>{row.row}</Table.Td>
                          <Table.Td>
                            <Text size="sm" ff="monospace">
                              {row.location}
                            </Text>
                          </Table.Td>
                          <Table.Td>{row.sku}</Table.Td>
                          {withLots && (
                            <Table.Td>
                              <Text size="sm" ff="monospace">
                                {row.lot ?? ""}
                              </Text>
                            </Table.Td>
                          )}
                          <Table.Td ta="right">{row.quantity}</Table.Td>
                          <Table.Td>
                            <Badge variant="light" color={row.outcome === "ok" ? "teal" : "red"}>
                              {t(`importOutcome_${row.outcome}`)}
                            </Badge>
                            {row.detail && (
                              <Text size="xs" c="dimmed">
                                {row.detail}
                              </Text>
                            )}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Table.ScrollContainer>
              </Stack>
            </PageSection>
          )}
        </>
      )}
    </>
  );
};

/**
 * The placements import (I61): one file across every site, of `sku`, `site`
 * (its code), `primary_location` (a full code), `reorder_point` and
 * `maximum`, and optionally `multiple` (manufacturing phase 2 M105). A dry run answers each row; Commit, offered only when every row
 * is ok, sets each placement, an empty cell clearing its field and a row
 * clearing all three removing it. The same file twice writes once.
 */
const PlacementsImport = () => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const [file, setFile] = useState<File | null>(null);
  const [checked, setChecked] = useState<{ result: InventoryPlacementImportResult; csv: ArrayBuffer }>();
  const [committed, setCommitted] = useState<{ result: InventoryPlacementImportResult; written: boolean }>();

  const dryRun = useMutation({
    mutationFn: async (upload: File) => {
      const csv = await readBytes(upload);
      return { result: (await importPlacements(csv, true)).result, csv };
    },
    onSuccess: (answer) => {
      setChecked(answer);
      setCommitted(undefined);
    },
  });
  const commit = useMutation({
    mutationFn: (csv: ArrayBuffer) => importPlacements(csv, false),
    onSuccess: async (answer) => {
      if (!answer.result.ok) {
        // Refused for its rows: nothing was written, and the rows say why.
        setChecked((now) => now && { ...now, result: answer.result });
        return;
      }
      setCommitted(answer);
      setChecked(undefined);
      setFile(null);
      await invalidate();
    },
  });
  const reset = () => {
    setChecked(undefined);
    setCommitted(undefined);
    dryRun.reset();
    commit.reset();
  };

  const rows = checked?.result.rows ?? [];
  const failing = rows.filter((row) => row.outcome !== "ok").length;

  return (
    <>
      {committed &&
        (committed.written ? (
          <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("placementsImported")}>
            {t("placementsImportedCounts", {
              set: committed.result.set ?? 0,
              cleared: committed.result.cleared ?? 0,
            })}
          </Alert>
        ) : (
          // The same file was imported before: the server wrote nothing this time.
          <Alert color="blue" icon={<IconCircleCheck size={16} />} title={t("placementsAlreadyImported")}>
            {t("placementsAlreadyImportedHint")}
          </Alert>
        ))}
      <PageSection>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("placementsImportHint")}
          </Text>
          <FileInput
            label={t("importFile")}
            placeholder={t("importFilePlaceholder")}
            accept=".csv,text/csv"
            clearable
            leftSection={<IconFileUpload size={16} />}
            value={file}
            onChange={(next) => {
              setFile(next);
              reset();
            }}
          />
          <Group justify="flex-end">
            <Button
              variant="light"
              disabled={!file}
              loading={dryRun.isPending}
              onClick={() => {
                if (!file) return;
                commit.reset();
                dryRun.mutate(file);
              }}
            >
              {t("importDryRun")}
            </Button>
          </Group>
        </Stack>
      </PageSection>
      {(dryRun.error || commit.error) && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importFailed")}>
          {refusalMessage(t, (dryRun.error ?? commit.error) as Error)}
        </Alert>
      )}
      {checked && (
        <PageSection>
          <Stack gap="md">
            <Group justify="space-between">
              <Text fw={500}>
                {checked.result.ok
                  ? t("placementsAllOk", { count: rows.length })
                  : t("placementsRowsFailing", { count: failing })}
              </Text>
              <Button
                onClick={() => commit.mutate(checked.csv)}
                disabled={!checked.result.ok}
                loading={commit.isPending}
              >
                {t("importCommit")}
              </Button>
            </Group>
            <Table.ScrollContainer minWidth={720}>
              <Table striped verticalSpacing="xs">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("importRow")}</Table.Th>
                    <Table.Th>{t("columnSku")}</Table.Th>
                    <Table.Th>{t("site")}</Table.Th>
                    <Table.Th>{t("primaryLocation")}</Table.Th>
                    <Table.Th ta="right">{t("reorderPoint")}</Table.Th>
                    <Table.Th ta="right">{t("maximumLevel")}</Table.Th>
                    <Table.Th ta="right">{t("multiple")}</Table.Th>
                    <Table.Th>{t("importOutcome")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.map((row) => (
                    <Table.Tr key={row.row} data-outcome={row.outcome}>
                      <Table.Td>{row.row}</Table.Td>
                      <Table.Td>{row.sku}</Table.Td>
                      <Table.Td>{row.site}</Table.Td>
                      <Table.Td>
                        <Text size="sm" ff="monospace">
                          {row.primaryLocation}
                        </Text>
                      </Table.Td>
                      <Table.Td ta="right">{row.reorderPoint}</Table.Td>
                      <Table.Td ta="right">{row.maximum}</Table.Td>
                      <Table.Td ta="right">{row.multiple ?? ""}</Table.Td>
                      <Table.Td>
                        <Badge variant="light" color={row.outcome === "ok" ? "teal" : "red"}>
                          {t(`placementOutcome_${row.outcome}`)}
                        </Badge>
                        {row.detail && (
                          <Text size="xs" c="dimmed">
                            {row.detail}
                          </Text>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Stack>
        </PageSection>
      )}
    </>
  );
};

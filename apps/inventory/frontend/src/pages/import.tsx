import { Alert, Anchor, Badge, Button, Card, FileInput, Group, Select, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconCircleCheck, IconDownload, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { IMPORT_HEADER, type InventoryImportResult, importOpeningBalances } from "../api/imports";
import { downloadBlob } from "../api/labels";
import { inventoryMetaQueryOptions } from "../api/meta";
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
 * The opening-balance import (I32): a site and a CSV of `location` (full
 * code), `sku` and `quantity`. A dry run answers each row's outcome; Commit,
 * offered only when every row is ok, posts one adjustment, reason Opening
 * balance, of every row — the same file twice is one movement. A template is
 * offered to start from. It needs `inventory:stock-adjust` and both views.
 */
export const ImportPage = () => {
  const { t } = useInventoryFormat();
  const invalidate = useInvalidateInventory();
  const meta = useQuery(inventoryMetaQueryOptions());
  const allowed = canImport(meta.data);
  const sites = useQuery({ ...sitesQueryOptions(), enabled: allowed });
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

  const template = () =>
    downloadBlob(new Blob([`${IMPORT_HEADER}\n`], { type: "text/csv;charset=utf-8" }), "opening-balances.csv");

  const rows = checked?.result.rows ?? [];
  const failing = rows.filter((row) => row.outcome !== "ok").length;

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
      ) : meta.isPending || (allowed && sites.isPending) ? (
        <ContentSkeleton rows={3} />
      ) : !allowed ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_import")}
        </Alert>
      ) : (
        <>
          {committed?.movementId !== undefined && (
            <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("importPosted")}>
              <Anchor renderRoot={(props) => <Link {...movementLinkOptions(committed.movementId ?? 0)} {...props} />}>
                {t("openPosted", { id: committed.movementId })}
              </Anchor>
            </Alert>
          )}
          <Card withBorder>
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
          </Card>
          {(dryRun.error || commit.error) && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importFailed")}>
              {refusalMessage(t, (dryRun.error ?? commit.error) as Error)}
            </Alert>
          )}
          {checked && (
            <Card withBorder>
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
            </Card>
          )}
        </>
      )}
    </Stack>
  );
};

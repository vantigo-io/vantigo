import { Alert, Anchor, Box, Button, Checkbox, Group, Modal, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useState } from "react";
import { type CustomerImportResult, downloadImportTemplate, importCustomers, saveCsv } from "../api/import-export";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { failedRowsCsv, parseCsv } from "../lib/csv";

/** What the failed rows of `name` are saved as: beside it, and saying what they are. */
const failedRowsFileName = (name: string) => `${name.replace(/\.csv$/i, "")}-failed-rows.csv`;

/**
 * The CSV import (customers import/export design D4), in three steps: pick a
 * file — dropped or chosen, the receipt dropzone's shape, with the template a
 * click away — then **Check**, the server's dry run, whose counts and errors are
 * shown and nothing kept; then **Import**, enabled once the check found a row
 * that would succeed, whose counts are shown again with, when rows failed,
 * **Download failed rows**: the original rows with an `error` column, built here
 * from the file the browser still holds, to be fixed and imported on their own.
 *
 * Changing the file or the duplicate flag throws the check away: a check is
 * about one file under one flag, and Import must never run on a different one.
 */
export const CustomerImportModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [allowDuplicateIdentity, setAllowDuplicateIdentity] = useState(false);
  const [check, setCheck] = useState<CustomerImportResult | null>(null);
  const [outcome, setOutcome] = useState<CustomerImportResult | null>(null);
  const [problem, setProblem] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);

  const reset = () => {
    setCheck(null);
    setOutcome(null);
    setProblem(null);
  };
  const close = () => {
    setFile(null);
    setAllowDuplicateIdentity(false);
    reset();
    onClose();
  };
  const choose = (next: File | undefined) => {
    if (!next) return;
    setFile(next);
    reset();
  };

  const messagesOf = (error: unknown): string[] => {
    if (error instanceof ApiValidationError) {
      const named = error.fields.file ?? Object.values(error.fields).flat();
      if (named.length > 0) return named;
    }
    return [(error as Error).message];
  };

  const counts = (result: CustomerImportResult) => ({
    rows: String(result.rows),
    created: String(result.created),
    updated: String(result.updated),
    failed: String(result.failed),
  });

  const run = async (dryRun: boolean) => {
    if (!file) return;
    setBusy(true);
    setProblem(null);
    try {
      const result = await importCustomers(file, { dryRun, allowDuplicateIdentity });
      if (dryRun) {
        setCheck(result);
      } else {
        setOutcome(result);
        notifications.show({
          color: result.failed > 0 ? "yellow" : "teal",
          title: t("importDone"),
          message: t("importDoneCounts", counts(result)),
        });
      }
    } catch (error) {
      setProblem(messagesOf(error));
      // A real run that failed part-way has committed the rows before the
      // failure. The check no longer describes what an import would do, and a
      // second click would create those rows twice: Import waits for a new
      // Check.
      if (!dryRun) setCheck(null);
    } finally {
      setBusy(false);
      // Whatever a real run did — all of it, or the rows before a failure —
      // the list, its counts and the filters' words may have moved.
      if (!dryRun) await queryClient.invalidateQueries({ queryKey: ["customers"] });
    }
  };

  const downloadTemplate = async () => {
    try {
      saveCsv(await downloadImportTemplate());
    } catch (error) {
      setProblem(messagesOf(error));
    }
  };

  const downloadFailedRows = async () => {
    if (!file || !outcome) return;
    const csv = failedRowsCsv(parseCsv(await file.text()), outcome.errors);
    saveCsv({ blob: new Blob([csv], { type: "text/csv;charset=utf-8" }), fileName: failedRowsFileName(file.name) });
  };

  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    choose(event.target.files?.[0]);
    event.target.value = "";
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    choose(event.dataTransfer?.files?.[0]);
  };

  const importable = check !== null && check.created + check.updated > 0;
  const shown = outcome ?? check;

  return (
    <Modal opened={opened} onClose={close} title={t("importTitle")} size="xl">
      <Stack gap="md">
        <Text size="sm">{t("importIntro")}</Text>
        <Anchor component="button" type="button" size="sm" onClick={() => void downloadTemplate()}>
          {t("importDownloadTemplate")}
        </Anchor>

        {!outcome && (
          <Box
            p="sm"
            style={{
              border: "1px dashed var(--mantine-color-gray-4)",
              borderRadius: "var(--mantine-radius-sm)",
              background: over ? "var(--mantine-color-gray-0)" : undefined,
            }}
            onDragOver={(event) => {
              event.preventDefault();
              setOver(true);
            }}
            onDragLeave={() => setOver(false)}
            onDrop={drop}
          >
            <Stack gap="xs">
              <Text size="sm" c="dimmed">
                {t("importDropFile")}
              </Text>
              <input type="file" accept=".csv,text/csv" aria-label={t("importChooseFile")} onChange={pick} />
              {file && <Text size="sm">{t("importChosenFile", { name: file.name })}</Text>}
            </Stack>
          </Box>
        )}

        <Checkbox
          label={t("importAllowDuplicateIdentity")}
          checked={allowDuplicateIdentity}
          disabled={outcome !== null}
          onChange={(event) => {
            setAllowDuplicateIdentity(event.currentTarget.checked);
            setCheck(null);
          }}
        />

        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotRun")}>
            {problem.map((message) => (
              <Text key={message} size="sm">
                {message}
              </Text>
            ))}
          </Alert>
        )}

        {shown && (
          <Stack gap="xs">
            <Text fw={600}>
              {outcome ? t("importDoneCounts", counts(outcome)) : t("importCheckCounts", counts(shown))}
            </Text>
            {!outcome && (
              <Text size="sm" c="dimmed">
                {importable ? t("importCheckIntro") : t("importNothingToImport")}
              </Text>
            )}
            {shown.errors.length > 0 && (
              <ScrollArea.Autosize mah={320}>
                <Table striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("importErrorRow")}</Table.Th>
                      <Table.Th>{t("importErrorColumn")}</Table.Th>
                      <Table.Th>{t("importErrorMessage")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {shown.errors.map((error, index) => (
                      // A row can fail on the same column twice; the index keeps them apart.
                      <Table.Tr key={`${error.row}:${error.column ?? ""}:${index}`}>
                        <Table.Td>{error.row}</Table.Td>
                        <Table.Td>{error.column ?? "—"}</Table.Td>
                        <Table.Td>{error.message}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </ScrollArea.Autosize>
            )}
          </Stack>
        )}

        {outcome && outcome.failed > 0 && (
          <Stack gap={4}>
            <Group>
              <Button variant="light" onClick={() => void downloadFailedRows()}>
                {t("importDownloadFailedRows")}
              </Button>
            </Group>
            <Text size="xs" c="dimmed">
              {t("importFailedRowsHint")}
            </Text>
          </Stack>
        )}

        <Group justify="flex-end">
          <Button variant="default" onClick={close}>
            {outcome ? t("importClose") : t("cancel")}
          </Button>
          {!outcome && (
            <>
              <Button
                variant="light"
                disabled={!file || busy}
                loading={busy && check === null}
                onClick={() => void run(true)}
              >
                {t("importCheck")}
              </Button>
              <Button disabled={!importable || busy} loading={busy && check !== null} onClick={() => void run(false)}>
                {t("importRun")}
              </Button>
            </>
          )}
        </Group>
      </Stack>
    </Modal>
  );
};

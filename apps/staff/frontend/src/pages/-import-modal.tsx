import { Alert, Box, Button, Group, Modal, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDownload, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, useId, useState } from "react";
import { downloadImportTemplate, exportPeople, importPeople, type StaffImportResult } from "../api/data";
import { type FileDownload, saveFile } from "../api/documents";
import { type ApiError, ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { failedRowsCsv, parseCsv } from "../lib/csv";

/** The server's own bound (`maxImportFileBytes`): a larger file is refused here rather than sent to be refused. */
const MAX_IMPORT_FILE_BYTES = 5 * 1024 * 1024;

/** The file refusals whose code the modal says in its own words; the server's sentences follow them. */
const WORDED_REFUSALS = ["import_sealed_columns", "import_not_semicolon", "import_too_many_rows", "import_too_large"];

/** What the failed rows of `name` are saved as: beside it, and saying what they are. */
const failedRowsFileName = (name: string) => `${name.replace(/\.csv$/i, "")}-failed-rows.csv`;

/**
 * The file as it was checked: its bytes, read once. The real run sends these
 * and the failed-rows file is built from them, so neither can differ from what
 * the check saw.
 */
interface HeldFile {
  name: string;
  type: string;
  bytes: ArrayBuffer;
}

const uploadOf = (held: HeldFile) => new File([held.bytes], held.name, { type: held.type || "text/csv" });

interface Problem {
  title: string;
  messages: string[];
}

/** The HTTP status a request failed with, or undefined when no answer came (a network failure). */
const statusOf = (error: unknown): number | undefined => {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : undefined;
};

/**
 * The register's import (design S19), in the customers import's three steps:
 * choose a file, **Check** — the server's dry run, whose counts and errors
 * are shown and nothing kept — then **Import**, enabled once the check found
 * a row that would be written, whose counts are shown again with, when rows
 * failed, **Download failed rows**: the original rows behind an `error`
 * column, built here from the bytes the check read; the import ignores that
 * column, so the file goes straight back in once fixed.
 *
 * A file refused whole is said in words by its code — the sealed columns
 * without `staff:personal-data` above all — followed by the server's own
 * sentences, which name the columns. The picker offers `.csv`, but the
 * server reads the bytes, not the name, so only the size is judged here.
 * A real run that got no answer, or a 5xx, may have committed rows before it
 * stopped (each row is its own transaction): the modal says they stay, and
 * Import waits for a new check rather than running the file twice. While a
 * real run is on its way the modal cannot be closed.
 */
export const ImportModal = ({ onClose, sealedAllowed }: { onClose: () => void; sealedAllowed: boolean }) => {
  const { t, formatters } = useI18n("staff");
  const queryClient = useQueryClient();
  const introId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [held, setHeld] = useState<HeldFile | null>(null);
  const [check, setCheck] = useState<StaffImportResult | null>(null);
  const [outcome, setOutcome] = useState<StaffImportResult | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [running, setRunning] = useState<"check" | "import" | null>(null);

  const close = () => {
    if (running !== "import") onClose();
  };

  const choose = (event: ChangeEvent<HTMLInputElement>) => {
    const next = event.target.files?.[0];
    event.target.value = "";
    // Held still while a check or an import runs: the answer is about the file sent.
    if (!next || running !== null) return;
    setHeld(null);
    setCheck(null);
    setOutcome(null);
    setProblem(null);
    if (next.size > MAX_IMPORT_FILE_BYTES) {
      setFile(null);
      setProblem({ title: t("importCouldNotRun"), messages: [t("importFileTooLarge", { name: next.name })] });
      return;
    }
    setFile(next);
  };

  const problemOf = (error: unknown): Problem => {
    if (error instanceof TypeError) return { title: t("importCouldNotRun"), messages: [t("importUnreachable")] };
    if (error instanceof ApiValidationError) {
      const code = (error as ApiError).code;
      const said = error.fields.file ?? Object.values(error.fields).flat();
      const worded = code && WORDED_REFUSALS.includes(code) ? [t(`importRefusal_${code}`)] : [];
      return { title: t("importRefused"), messages: [...worded, ...said] };
    }
    return { title: t("importCouldNotRun"), messages: [(error as Error).message] };
  };

  const run = async (dryRun: boolean) => {
    setRunning(dryRun ? "check" : "import");
    setProblem(null);
    try {
      let source = held;
      if (dryRun) {
        if (!file) return;
        try {
          source = { name: file.name, type: file.type, bytes: await file.arrayBuffer() };
        } catch {
          setProblem({ title: t("importCouldNotRun"), messages: [t("importFileUnreadable")] });
          return;
        }
      }
      if (!source) return;
      const result = await importPeople(uploadOf(source), dryRun);
      if (dryRun) {
        setHeld(source);
        setCheck(result);
      } else {
        setOutcome(result);
        notifications.show({
          color: result.failed > 0 ? "yellow" : "teal",
          title: t("importDone"),
          message: t("importDoneSummary", {
            created: formatters.formatNumber(result.created),
            updated: formatters.formatNumber(result.updated),
            failed: formatters.formatNumber(result.failed),
          }),
        });
      }
    } catch (error) {
      const status = statusOf(error);
      // No answer, or the server failing, after a real run began: the rows
      // before the failure may be committed, and the check no longer
      // describes what an import of this file would do.
      if (!dryRun && (error instanceof TypeError || (status !== undefined && status >= 500))) {
        setCheck(null);
        setProblem({ title: t("importCouldNotRun"), messages: [t("importCutShort")] });
      } else setProblem(problemOf(error));
    } finally {
      setRunning(null);
      // Whatever a real run did — all of it, or the rows before a failure —
      // the people and their counts may have moved.
      if (!dryRun) await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    }
  };

  const downloadFailedRows = () => {
    if (!held || !outcome) return;
    const csv = failedRowsCsv(parseCsv(new TextDecoder("utf-8").decode(held.bytes)), outcome.errors);
    saveFile({ blob: new Blob([csv], { type: "text/csv;charset=utf-8" }), fileName: failedRowsFileName(held.name) });
  };

  const figures = (result: StaffImportResult, done: boolean) => [
    { label: t("importCountRows"), value: result.rows },
    { label: t(done ? "importCountCreated" : "importCountWouldCreate"), value: result.created },
    { label: t(done ? "importCountUpdated" : "importCountWouldUpdate"), value: result.updated },
    { label: t(done ? "importCountFailed" : "importCountHaveErrors"), value: result.failed },
  ];

  const importable = check !== null && check.created + check.updated > 0;
  const shown = outcome ?? check;

  return (
    <Modal
      opened
      onClose={close}
      title={t("importPeople")}
      size="xl"
      withCloseButton={running !== "import"}
      closeOnEscape={running !== "import"}
      closeOnClickOutside={running !== "import"}
    >
      <Stack gap="md">
        <Text id={introId} size="sm">
          {t("importIntro")}
        </Text>
        <Text size="sm">{t("importFailedRowsIntro")}</Text>
        {!sealedAllowed && (
          <Text size="sm" c="dimmed">
            {t("importSealedNote")}
          </Text>
        )}

        {!outcome && (
          <Stack gap="xs">
            <input
              type="file"
              accept=".csv,text/csv"
              aria-label={t("importChooseFile")}
              aria-describedby={introId}
              disabled={running !== null}
              onChange={choose}
            />
            {file && <Text size="sm">{t("importChosenFile", { name: file.name })}</Text>}
          </Stack>
        )}

        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={problem.title}>
            {problem.messages.map((message, index) => (
              // Two refusals can be the same sentence; the list is replaced whole, so the index keys it.
              <Text key={index} size="sm">
                {message}
              </Text>
            ))}
          </Alert>
        )}

        {/* The counts are announced when a check or an import answers, without a focus move; the table is not. */}
        <Box role="status" aria-live="polite">
          {running === "import" && <Text size="sm">{t("importRunning")}</Text>}
          {shown && (
            <Stack gap="xs">
              <Group gap="xl">
                {figures(shown, outcome !== null).map(({ label, value }) => (
                  <div key={label}>
                    <Text size="xs" c="dimmed">
                      {label}
                    </Text>
                    <Text fw={600}>{formatters.formatNumber(value)}</Text>
                  </div>
                ))}
              </Group>
              {!outcome && (
                <Text size="sm" c="dimmed">
                  {importable ? t("importCheckIntro") : t("importNothingToImport")}
                </Text>
              )}
            </Stack>
          )}
        </Box>
        {shown && shown.errors.length > 0 && (
          <ScrollArea.Autosize mah={320}>
            <Table striped aria-label={t("importErrorsCaption")}>
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

        {outcome && outcome.failed > 0 && (
          <Stack gap={4}>
            <Group>
              <Button variant="light" onClick={downloadFailedRows}>
                {t("importDownloadFailedRows")}
              </Button>
            </Group>
            <Text size="xs" c="dimmed">
              {t("importFailedRowsHint")}
            </Text>
          </Stack>
        )}

        <Group justify="flex-end">
          <Button variant="default" disabled={running === "import"} onClick={close}>
            {outcome ? t("importClose") : t("cancel")}
          </Button>
          {!outcome && (
            <>
              <Button
                variant="light"
                disabled={!file || running !== null}
                loading={running === "check"}
                onClick={() => void run(true)}
              >
                {t("importCheck")}
              </Button>
              <Button
                disabled={!importable || running !== null}
                loading={running === "import"}
                onClick={() => void run(false)}
              >
                {t("importRun")}
              </Button>
            </>
          )}
        </Group>
      </Stack>
    </Modal>
  );
};

/** A file fetched and saved under the server's name; a refusal said under `failed`, `export_too_many` in words. */
const useDownload = (failed: string) => {
  const { t } = useI18n("staff");
  return useMutation({
    mutationFn: (fetchFile: () => Promise<FileDownload>) => fetchFile(),
    onSuccess: saveFile,
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t(failed),
        message: (error as ApiError).code === "export_too_many" ? t("exportTooMany") : error.message,
      }),
  });
};

/**
 * The register's file in Settings (design S19), `staff:manage`'s: the import
 * modal, the export — every person, never a sealed value, saved under the
 * server's name (`export_too_many` said in words) — and the template, whose
 * sealed columns the server includes only for `staff:personal-data`.
 */
export const RegisterDataCard = ({ sealedAllowed }: { sealedAllowed: boolean }) => {
  const { t } = useI18n("staff");
  const [importing, setImporting] = useState(false);
  const exporting = useDownload("couldNotExportPeople");
  const template = useDownload("couldNotDownloadTemplate");

  return (
    <FieldGroup
      title={t("dataTitle")}
      description={t("dataDescription")}
      actions={
        <Group gap="xs">
          <Button leftSection={<IconFileUpload size={16} />} onClick={() => setImporting(true)}>
            {t("importPeople")}
          </Button>
          <Button
            variant="light"
            leftSection={<IconDownload size={16} />}
            loading={exporting.isPending}
            onClick={() => exporting.mutate(exportPeople)}
          >
            {t("exportPeople")}
          </Button>
          <Button variant="subtle" loading={template.isPending} onClick={() => template.mutate(downloadImportTemplate)}>
            {t("downloadTemplate")}
          </Button>
        </Group>
      }
    >
      {importing && <ImportModal sealedAllowed={sealedAllowed} onClose={() => setImporting(false)} />}
    </FieldGroup>
  );
};

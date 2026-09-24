import { Alert, Anchor, Box, Button, Checkbox, Group, Modal, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useId, useRef, useState } from "react";
import {
  type CustomerImportResult,
  downloadImportTemplate,
  importCustomers,
  isSessionExpired,
  saveCsv,
} from "../api/import-export";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { failedRowsCsv, parseCsv } from "../lib/csv";

/** The server's own limit (`maxImportFileBytes`): a larger file is refused here rather than sent to be refused. */
const MAX_IMPORT_FILE_BYTES = 5 * 1024 * 1024;

/** What the failed rows of `name` are saved as: beside it, and saying what they are. */
const failedRowsFileName = (name: string) => `${name.replace(/\.csv$/i, "")}-failed-rows.csv`;

/**
 * The file as it was checked: its bytes, read once. The real run sends these
 * and the failed-rows file is built from them, so neither can differ from what
 * the check saw — nor fail because the file on disk changed or went away
 * (Chrome's `NotReadableError`) after it was picked.
 */
interface HeldFile {
  name: string;
  type: string;
  bytes: ArrayBuffer;
}

const uploadOf = (held: HeldFile) => new File([held.bytes], held.name, { type: held.type || "text/csv" });

/** What went wrong, and whether a real run may have saved rows before it did. */
interface Problem {
  messages: string[];
  maybeSaved: boolean;
}

/**
 * The CSV import (customers import/export design D4), in three steps: pick a
 * file — dropped or chosen, the receipt dropzone's shape, with the template a
 * click away — then **Check**, the server's dry run, whose counts and errors are
 * shown and nothing kept; then **Import**, enabled once the check found a row
 * that would succeed, whose counts are shown again with, when rows failed,
 * **Download failed rows**: the original rows with an `error` column, built here
 * from the bytes the check read, to be fixed and imported on their own.
 *
 * Changing the file or the duplicate flag throws the check away: a check is
 * about one file under one flag, and Import must never run on a different one.
 *
 * The page keeps this component mounted and only toggles `opened`, so a
 * request that answers after a close would land in the next opening. Each
 * close starts a new generation, and an answer from an older one is dropped. A
 * check may be abandoned that way; a real run may not — it is changing
 * customers on the server whatever the modal does, and its result (the failed
 * rows above all) exists only here — so while one runs the modal cannot be
 * closed.
 */
export const CustomerImportModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const dropTextId = useId();
  const generation = useRef(0);
  const [file, setFile] = useState<File | null>(null);
  const [held, setHeld] = useState<HeldFile | null>(null);
  const [allowDuplicateIdentity, setAllowDuplicateIdentity] = useState(false);
  const [check, setCheck] = useState<CustomerImportResult | null>(null);
  const [outcome, setOutcome] = useState<CustomerImportResult | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [running, setRunning] = useState<"check" | "import" | null>(null);
  const [over, setOver] = useState(false);

  const shownProblem = (messages: string[], maybeSaved = false) => setProblem({ messages, maybeSaved });

  /** Forgets every answer so far, and any still on its way. */
  const reset = () => {
    generation.current += 1;
    setRunning(null);
    setHeld(null);
    setCheck(null);
    setOutcome(null);
    setProblem(null);
  };
  const close = () => {
    if (running === "import") return;
    setFile(null);
    setAllowDuplicateIdentity(false);
    reset();
    onClose();
  };
  const choose = (next: File | undefined) => {
    if (!next || running === "import") return;
    reset();
    if (next.size > MAX_IMPORT_FILE_BYTES) {
      setFile(null);
      shownProblem([t("importFileTooLarge", { name: next.name })]);
      return;
    }
    setFile(next);
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
    const current = generation.current;
    const stale = () => current !== generation.current;
    setRunning(dryRun ? "check" : "import");
    setProblem(null);
    try {
      let source = held;
      if (dryRun) {
        if (!file) return;
        try {
          source = { name: file.name, type: file.type, bytes: await file.arrayBuffer() };
        } catch {
          if (!stale()) shownProblem([t("importFileUnreadable")]);
          return;
        }
      }
      if (!source) return;
      const result = await importCustomers(uploadOf(source), { dryRun, allowDuplicateIdentity });
      if (stale()) return;
      if (dryRun) {
        setHeld(source);
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
      if (stale()) return;
      // A real run that failed part-way has committed the rows before the
      // failure. The check no longer describes what an import would do, and a
      // second click would create those rows twice: Import waits for a new
      // Check, and the person is told a new check may call saved rows new.
      if (!dryRun) setCheck(null);
      // An expired session has signed the person out; nothing here to show.
      if (!isSessionExpired(error)) shownProblem(messagesOf(error), !dryRun);
    } finally {
      if (!stale()) setRunning(null);
      // Whatever a real run did — all of it, or the rows before a failure —
      // the list, its counts and the filters' words may have moved.
      if (!dryRun) await queryClient.invalidateQueries({ queryKey: ["customers"] });
    }
  };

  const downloadTemplate = async () => {
    try {
      saveCsv(await downloadImportTemplate());
    } catch (error) {
      if (!isSessionExpired(error)) shownProblem(messagesOf(error));
    }
  };

  const downloadFailedRows = () => {
    if (!held || !outcome) return;
    const csv = failedRowsCsv(parseCsv(new TextDecoder("utf-8").decode(held.bytes)), outcome.errors);
    saveCsv({ blob: new Blob([csv], { type: "text/csv;charset=utf-8" }), fileName: failedRowsFileName(held.name) });
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
              <Text id={dropTextId} size="sm" c="dimmed">
                {t("importDropFile")}
              </Text>
              <input
                type="file"
                accept=".csv,text/csv"
                aria-label={t("importChooseFile")}
                aria-describedby={dropTextId}
                disabled={running === "import"}
                onChange={pick}
              />
              {file && <Text size="sm">{t("importChosenFile", { name: file.name })}</Text>}
            </Stack>
          </Box>
        )}

        <Checkbox
          label={t("importAllowDuplicateIdentity")}
          checked={allowDuplicateIdentity}
          disabled={outcome !== null || running === "import"}
          onChange={(event) => {
            setAllowDuplicateIdentity(event.currentTarget.checked);
            // A check under the other flag, answered or on its way, is not this one.
            generation.current += 1;
            setRunning(null);
            setHeld(null);
            setCheck(null);
          }}
        />

        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotRun")}>
            {problem.messages.map((message, index) => (
              // Two refusals can be the same sentence; the list is replaced whole,
              // never reordered, so the index keys it.
              <Text key={index} size="sm">
                {message}
              </Text>
            ))}
            {problem.maybeSaved && (
              <Text size="sm" fw={600} mt="xs">
                {t("importMayBeSaved")}
              </Text>
            )}
          </Alert>
        )}

        {/* Announced when a check or an import answers: the result appears without a focus move. */}
        <Box role="status" aria-live="polite">
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
                    <Table.Caption>{t("importErrorsCaption")}</Table.Caption>
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
        </Box>

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

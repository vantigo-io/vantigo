import { Alert, Button, FileInput, Group, Modal, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useRef, useState } from "react";
import { ApiValidationError } from "../api/request";
import {
  importSpotPrices,
  MAX_SPOT_PRICE_IMPORT_FILE_BYTES,
  type SpotPriceImportResult,
  spotPricesQueryKey,
} from "../api/spot-prices";
import { MARKET_TIME_ZONE } from "../lib/market-time";
import "../i18n";

/** A refusal of the file itself names its problem on the `file` field; anything else as the server words it. */
const problem = (error: Error) =>
  error instanceof ApiValidationError ? Object.values(error.errors).flat().join(" ") || error.message : error.message;

/**
 * **Import prices** (energy consumption billing design D2): spot prices from
 * a file of a fixed layout, so there is nothing to map — the file is checked
 * as soon as it is chosen (a dry run: the rows, each area's counts and range,
 * the first errors), and imported, every row or none, when the check found no
 * error. The coverage and the chart are read again after an import.
 */
export const ImportPricesModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t, formatters } = useI18n("energy");
  const client = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [check, setCheck] = useState<SpotPriceImportResult | null>(null);
  const [done, setDone] = useState<SpotPriceImportResult | null>(null);
  // Each opening is a session of its own: an answer to a request sent in an
  // earlier one (the modal was closed while it was pending) is dropped.
  const session = useRef(0);
  const [wasOpened, setWasOpened] = useState(opened);

  const reset = () => {
    setFile(null);
    setCheck(null);
    setDone(null);
  };
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (opened) reset();
  }

  const checkMutation = useMutation({
    mutationFn: ({ file: chosen }: { file: File; session: number }) => importSpotPrices(chosen, true),
    onSuccess: (result, sent) => {
      if (sent.session === session.current) setCheck(result);
    },
    onError: (error, sent) => {
      if (sent.session !== session.current) return;
      notifications.show({ color: "red", title: t("importPricesCouldNotCheck"), message: problem(error) });
    },
  });
  const importMutation = useMutation({
    mutationFn: ({ file: chosen }: { file: File; session: number }) => importSpotPrices(chosen, false),
    onSuccess: (result, sent) => {
      void client.invalidateQueries({ queryKey: spotPricesQueryKey });
      if (sent.session !== session.current) return;
      setCheck(null);
      setDone(result);
    },
    onError: (error, sent) => {
      if (sent.session !== session.current) return;
      // 422: a row has an error now that the check did not see (the stored
      // prices changed in between); 409: another import of the same area ran
      // at the same time. Either way the check is run again to show the file
      // against what is stored now.
      const status = (error as { status?: number }).status;
      const stale = status === 422 || status === 409;
      setCheck(null);
      notifications.show({
        color: "red",
        title: t("importPricesCouldNotImport"),
        message: status === 422 ? t("importPricesNowHasErrors") : problem(error),
      });
      if (stale) checkMutation.mutate(sent);
    },
  });

  const close = () => {
    session.current += 1;
    checkMutation.reset();
    importMutation.reset();
    reset();
    onClose();
  };

  const choose = (chosen: File | null) => {
    reset();
    setFile(chosen);
    if (!chosen) return;
    if (chosen.size > MAX_SPOT_PRICE_IMPORT_FILE_BYTES) {
      notifications.show({ color: "red", title: t("importPricesCouldNotCheck"), message: t("importPricesTooLarge") });
      return;
    }
    checkMutation.mutate({ file: chosen, session: session.current });
  };

  const result = done ?? check;
  const changes = (check?.areas ?? []).reduce((sum, area) => sum + area.inserted + area.replaced, 0);
  const canImport = !!check && check.errorCount === 0 && changes > 0;
  const date = (value: string | undefined) =>
    value ? formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short", timeZone: MARKET_TIME_ZONE }) : "";

  return (
    <Modal opened={opened} onClose={close} title={t("importPrices")} size="xl" centered>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("importPricesIntro")}
        </Text>
        <FileInput
          label={t("importReadingsFile")}
          placeholder={t("importReadingsChooseFile")}
          accept=".csv,text/csv,text/plain"
          leftSection={<IconFileUpload size={16} />}
          value={file}
          onChange={choose}
          clearable
          disabled={checkMutation.isPending || importMutation.isPending || !!done}
        />
        {done && (
          <Alert color="teal" icon={<IconCircleCheck size={16} />} title={t("importPricesDone")}>
            {t("importPricesDoneMessage", { count: done.rows })}
          </Alert>
        )}
        {result && (
          <Stack gap="xs">
            {!done && <Text fw={600}>{t("importPricesDryRun", { count: result.rows })}</Text>}
            {result.areas.length > 0 && (
              <Table.ScrollContainer minWidth={560}>
                <Table striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("priceArea")}</Table.Th>
                      <Table.Th>{t("importPricesInserted")}</Table.Th>
                      <Table.Th>{t("importPricesReplaced")}</Table.Th>
                      <Table.Th>{t("importPricesUnchanged")}</Table.Th>
                      <Table.Th>{t("importPricesRange")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {result.areas.map((area) => (
                      <Table.Tr key={area.priceArea}>
                        <Table.Td>{area.priceArea}</Table.Td>
                        <Table.Td>{area.inserted}</Table.Td>
                        <Table.Td>{area.replaced}</Table.Td>
                        <Table.Td>{area.unchanged}</Table.Td>
                        <Table.Td>
                          {area.rangeStart && area.rangeEnd
                            ? t("importPricesRangeValue", { start: date(area.rangeStart), end: date(area.rangeEnd) })
                            : t("notAvailable")}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            )}
            {result.errorCount > 0 ? (
              <>
                <Alert color="red" icon={<IconAlertCircle size={16} />}>
                  {t("importReadingsHasErrors", { count: result.errorCount })}{" "}
                  {result.errorCount > result.errors.length &&
                    t("importReadingsErrorsShown", { shown: result.errors.length })}
                </Alert>
                <ScrollArea.Autosize mah={280}>
                  <Table striped>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>{t("importReadingsRow")}</Table.Th>
                        <Table.Th>{t("importReadingsColumn")}</Table.Th>
                        <Table.Th>{t("importReadingsProblem")}</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {result.errors.map((error, index) => (
                        <Table.Tr key={`${error.row}-${error.column ?? "row"}-${index}`}>
                          <Table.Td>{error.row}</Table.Td>
                          <Table.Td>{error.column === undefined ? "" : error.column + 1}</Table.Td>
                          <Table.Td>{error.message}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </ScrollArea.Autosize>
              </>
            ) : (
              !done && changes === 0 && <Text size="sm">{t("importPricesNothingNew")}</Text>
            )}
          </Stack>
        )}
        <Group justify="flex-end">
          {done ? (
            <Button onClick={close}>{t("close")}</Button>
          ) : (
            <>
              <Button variant="default" onClick={close}>
                {t("cancel")}
              </Button>
              <Button
                disabled={!canImport}
                loading={importMutation.isPending || checkMutation.isPending}
                onClick={() => file && importMutation.mutate({ file, session: session.current })}
              >
                {t("importReadingsImport")}
              </Button>
            </>
          )}
        </Group>
      </Stack>
    </Modal>
  );
};

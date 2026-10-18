import {
  Alert,
  Button,
  FileInput,
  Group,
  Modal,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconFileUpload } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type ConsumptionImportMapping,
  type ConsumptionImportResult,
  importConsumption,
  MAX_CONSUMPTION_IMPORT_FILE_BYTES,
} from "../api/energy";
import { columnLabel } from "../lib/import-columns";
import { MARKET_TIME_ZONE } from "../lib/market-time";
import "../i18n";

type Resolution = NonNullable<ConsumptionImportMapping["resolution"]>;

/** The mapping as the form holds it: each column as a Select's value, "" for none. */
interface MappingForm {
  start: string;
  end: string;
  resolution: Resolution;
  quantityKwh: string;
  quality: string;
  gsrn: string;
}

const fromSuggested = (suggested: ConsumptionImportMapping): MappingForm => {
  const value = (column: number | undefined) => (column === undefined ? "" : String(column));
  return {
    start: value(suggested.start),
    end: value(suggested.end),
    resolution: suggested.resolution ?? "PT1H",
    quantityKwh: value(suggested.quantityKwh),
    quality: value(suggested.quality),
    gsrn: value(suggested.gsrn),
  };
};

const toMapping = (form: MappingForm): ConsumptionImportMapping => {
  const column = (value: string) => (value === "" ? undefined : Number(value));
  return {
    start: column(form.start),
    ...(form.end === "" ? { resolution: form.resolution } : { end: column(form.end) }),
    quantityKwh: column(form.quantityKwh),
    quality: column(form.quality),
    gsrn: column(form.gsrn),
  };
};

/**
 * **Import readings** (energy import design D8): one metering point's readings
 * from a file, in three calls to one endpoint — the file read for its header
 * and the suggested mapping, the check (a dry run: the counts, the range and
 * the errors), and the import, confirmed with what it adds and replaces. The
 * server writes every row or none; a check with errors offers no import.
 */
export const ImportReadingsModal = ({
  meteringPointId,
  opened,
  onClose,
}: {
  meteringPointId: number;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t, formatters } = useI18n("energy");
  const client = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [read, setRead] = useState<ConsumptionImportResult | null>(null);
  const [form, setForm] = useState<MappingForm | null>(null);
  const [check, setCheck] = useState<ConsumptionImportResult | null>(null);
  const [confirming, setConfirming] = useState(false);

  const reset = () => {
    setFile(null);
    setRead(null);
    setForm(null);
    setCheck(null);
    setConfirming(false);
  };
  const close = () => {
    reset();
    onClose();
  };

  const readMutation = useMutation({
    mutationFn: (chosen: File) => importConsumption(meteringPointId, chosen, null),
    onSuccess: (result) => {
      setRead(result);
      setForm(fromSuggested(result.suggested));
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("importReadingsCouldNotRead"), message: error.message }),
  });
  const checkMutation = useMutation({
    mutationFn: ({ chosen, mapping }: { chosen: File; mapping: ConsumptionImportMapping }) =>
      importConsumption(meteringPointId, chosen, mapping, true),
    onSuccess: setCheck,
    onError: (error) =>
      notifications.show({ color: "red", title: t("importReadingsCouldNotCheck"), message: error.message }),
  });
  const importMutation = useMutation({
    mutationFn: ({ chosen, mapping }: { chosen: File; mapping: ConsumptionImportMapping }) =>
      importConsumption(meteringPointId, chosen, mapping, false),
    onSuccess: (result) => {
      void client.invalidateQueries({ queryKey: ["energy", "metering-points", meteringPointId, "consumption"] });
      notifications.show({
        color: "teal",
        title: t("importReadingsDone"),
        message: t("importReadingsDoneMessage", { new: result.new, replacing: result.replacing }),
      });
      close();
    },
    onError: (error) => {
      // 422: a row has an error now that the check did not see; the check is
      // stale, so it is taken away and has to be run again.
      const stale = (error as { status?: number }).status === 422;
      if (stale) {
        setCheck(null);
        setConfirming(false);
      }
      notifications.show({
        color: "red",
        title: t("importReadingsCouldNotImport"),
        message: stale ? t("importReadingsNowHasErrors") : error.message,
      });
    },
  });

  const choose = (chosen: File | null) => {
    reset();
    setFile(chosen);
    if (!chosen) return;
    if (chosen.size > MAX_CONSUMPTION_IMPORT_FILE_BYTES) {
      notifications.show({
        color: "red",
        title: t("importReadingsCouldNotRead"),
        message: t("importReadingsTooLarge"),
      });
      return;
    }
    readMutation.mutate(chosen);
  };
  const change = (field: keyof MappingForm, value: string | null) => {
    if (!form) return;
    setForm({ ...form, [field]: value ?? "" });
    setCheck(null);
    setConfirming(false);
  };

  const header = read?.header ?? [];
  const columns = header.map((_, index) => ({
    value: String(index),
    label: columnLabel(header, index, t),
  }));
  const canCheck = !!file && !!form && form.start !== "" && form.quantityKwh !== "";
  const canImport = !!check && check.errorCount === 0 && check.new + check.replacing > 0;
  const date = (value: string | undefined) =>
    value ? formatters.formatDate(value, { dateStyle: "medium", timeStyle: "short", timeZone: MARKET_TIME_ZONE }) : "";

  return (
    <Modal opened={opened} onClose={close} title={t("importReadings")} size="xl" centered>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("importReadingsIntro")}
        </Text>
        <FileInput
          label={t("importReadingsFile")}
          placeholder={t("importReadingsChooseFile")}
          accept=".csv,text/csv,text/plain"
          leftSection={<IconFileUpload size={16} />}
          value={file}
          onChange={choose}
          clearable
          description={read ? t("importReadingsRows", { count: read.rows }) : undefined}
          disabled={readMutation.isPending}
        />
        {form && (
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Select
              label={t("importReadingsStart")}
              data={columns}
              value={form.start || null}
              onChange={(value) => change("start", value)}
              withAsterisk
            />
            <Select
              label={t("importReadingsQuantity")}
              data={columns}
              value={form.quantityKwh || null}
              onChange={(value) => change("quantityKwh", value)}
              withAsterisk
            />
            <Select
              label={t("importReadingsEnd")}
              placeholder={t("importReadingsNoEnd")}
              data={columns}
              value={form.end || null}
              onChange={(value) => change("end", value)}
              clearable
            />
            {form.end === "" && (
              <Select
                label={t("importReadingsResolution")}
                data={[
                  { value: "PT1H", label: t("importReadingsResolutionHour") },
                  { value: "PT15M", label: t("importReadingsResolutionQuarter") },
                ]}
                value={form.resolution}
                onChange={(value) => change("resolution", value ?? "PT1H")}
                allowDeselect={false}
              />
            )}
            <Select
              label={t("importReadingsQuality")}
              placeholder={t("importReadingsNoQuality")}
              data={columns}
              value={form.quality || null}
              onChange={(value) => change("quality", value)}
              clearable
            />
            <Select
              label={t("importReadingsGsrn")}
              placeholder={t("importReadingsNoGsrn")}
              data={columns}
              value={form.gsrn || null}
              onChange={(value) => change("gsrn", value)}
              clearable
            />
          </SimpleGrid>
        )}
        {check && (
          <Stack gap="xs">
            <Text fw={600}>
              {t("importReadingsCounts", { new: check.new, replacing: check.replacing, unchanged: check.unchanged })}
            </Text>
            {check.rangeStart && check.rangeEnd && (
              <Text size="sm">
                {t("importReadingsRange", { start: date(check.rangeStart), end: date(check.rangeEnd) })}
              </Text>
            )}
            {check.errorCount > 0 ? (
              <>
                <Alert color="red" icon={<IconAlertCircle size={16} />}>
                  {t("importReadingsHasErrors", { count: check.errorCount })}{" "}
                  {check.errorCount > check.errors.length &&
                    t("importReadingsErrorsShown", { shown: check.errors.length })}
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
                      {check.errors.map((error, index) => (
                        <Table.Tr key={`${error.row}-${error.column ?? "row"}-${index}`}>
                          <Table.Td>{error.row}</Table.Td>
                          <Table.Td>{error.column === undefined ? "" : columnLabel(header, error.column, t)}</Table.Td>
                          <Table.Td>{error.message}</Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </ScrollArea.Autosize>
              </>
            ) : (
              check.new + check.replacing === 0 && <Text size="sm">{t("importReadingsNothingNew")}</Text>
            )}
          </Stack>
        )}
        {confirming && check ? (
          <Alert color="blue" title={t("importReadingsConfirm", { new: check.new, replacing: check.replacing })}>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setConfirming(false)}>
                {t("importReadingsBack")}
              </Button>
              <Button
                loading={importMutation.isPending}
                onClick={() => file && form && importMutation.mutate({ chosen: file, mapping: toMapping(form) })}
              >
                {t("importReadingsImport")}
              </Button>
            </Group>
          </Alert>
        ) : (
          <Group justify="flex-end">
            <Button variant="default" onClick={close}>
              {t("cancel")}
            </Button>
            <Button
              variant={canImport ? "default" : "filled"}
              disabled={!canCheck}
              loading={checkMutation.isPending}
              onClick={() => file && form && checkMutation.mutate({ chosen: file, mapping: toMapping(form) })}
            >
              {t("importReadingsCheck")}
            </Button>
            <Button disabled={!canImport} onClick={() => setConfirming(true)}>
              {t("importReadingsImport")}
            </Button>
          </Group>
        )}
      </Stack>
    </Modal>
  );
};

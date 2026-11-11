import {
  Alert,
  Button,
  Group,
  Modal,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Textarea,
  TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useState } from "react";
import type { ManufacturingPerson } from "../api/people";
import {
  addTimeLog,
  correctTimeLog,
  downtimeReasons,
  type ManufacturingDowntimeReason,
  type ManufacturingTimeLog,
  type ManufacturingTimeLogKind,
  type ManufacturingWorkOrder,
  timeLogKinds,
} from "../api/shop-floor";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";
import {
  TIME_LOG_NOTE_MAX,
  type TimeLogValues,
  timeLogBody,
  timeLogProblems,
  timeLogValuesOf,
} from "../lib/time-log-body";
import { PersonPicker } from "./person-picker";

export interface TimeLogDialogProps {
  /** The log to correct; absent, the dialog adds a missing log to one of `workOrders`. */
  log?: ManufacturingTimeLog;
  workOrders: readonly ManufacturingWorkOrder[];
  opened: boolean;
  onClose: () => void;
}

/**
 * Correct a time log, or add a missing one (M29, M29a), for a supervisor
 * (`manufacturing:orders-manage`): the start and the end, the quantity, and
 * a note saying why — kept in the order's timeline. The end must come after
 * the start and not in the future; the server also refuses an overlap with
 * the person's other logs (`log_overlaps`), a log older than 60 days
 * (`log_locked`) and an order done or cancelled (`order_closed`).
 */
export const TimeLogDialog = ({ log, workOrders, opened, onClose }: TimeLogDialogProps) => {
  const { t } = useManufacturingFormat();
  return (
    <Modal opened={opened} onClose={onClose} title={log ? t("correctLogTitle") : t("addMissingLog")} size="lg">
      {opened && <TimeLogForm log={log} workOrders={workOrders} onClose={onClose} />}
    </Modal>
  );
};

const TimeLogForm = ({ log, workOrders, onClose }: Omit<TimeLogDialogProps, "opened">) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const [values, setValues] = useState<TimeLogValues>(() => timeLogValuesOf(log));
  const [workOrderId, setWorkOrderId] = useState<string | null>(
    workOrders.length === 1 ? String(workOrders[0].id) : null,
  );
  const [person, setPerson] = useState<ManufacturingPerson>();
  const [kind, setKind] = useState<ManufacturingTimeLogKind>("productive");
  const [reason, setReason] = useState<ManufacturingDowntimeReason | null>(null);
  const [touched, setTouched] = useState(false);
  const [problem, setProblem] = useState<string>();
  const [sending, setSending] = useState(false);

  const problems = timeLogProblems(values);
  const missing = {
    workOrder: !log && workOrderId === null,
    person: !log && person === undefined,
    reason: !log && kind === "downtime" && reason === null,
  };
  const invalid = Object.keys(problems).length > 0 || Object.values(missing).some(Boolean);
  const shown = (field: keyof TimeLogValues) => {
    const rule = problems[field];
    return touched && rule ? t(`problem_${rule}`) : undefined;
  };
  const set = (field: keyof TimeLogValues, value: string) => setValues((current) => ({ ...current, [field]: value }));

  const save = async () => {
    setTouched(true);
    if (invalid) return;
    setSending(true);
    setProblem(undefined);
    try {
      const body = timeLogBody(values);
      if (log) await correctTimeLog(log.id, body);
      else
        await addTimeLog(Number(workOrderId), {
          ...body,
          userId: person?.id ?? "",
          kind,
          ...(kind === "downtime" ? { reason } : {}),
        });
      notifications.show({ color: "teal", title: log ? t("logCorrected") : t("logAdded"), message: "" });
      await invalidate();
      onClose();
    } catch (error) {
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setSending(false);
    }
  };

  return (
    <Stack gap="md">
      {!log && (
        <>
          <Select
            label={t("workOrder")}
            data={workOrders.map((one) => ({
              value: String(one.id),
              label: t("operationOf", { sequence: one.sequence, name: one.name }),
            }))}
            value={workOrderId}
            onChange={setWorkOrderId}
            error={touched && missing.workOrder ? t("problem_required") : undefined}
          />
          <PersonPicker label={t("person")} value={person} onChange={setPerson} />
          {touched && missing.person && (
            <Alert color="red" variant="light" p="xs">
              {t("problem_personRequired")}
            </Alert>
          )}
          <SegmentedControl
            value={kind}
            onChange={(value) => setKind(value as ManufacturingTimeLogKind)}
            data={timeLogKinds.map((value) => ({ value, label: t(`logKind_${value}`) }))}
          />
          {kind === "downtime" && (
            <Select
              label={t("blockReason")}
              data={downtimeReasons.map((value) => ({ value, label: t(`reason_${value}`) }))}
              value={reason}
              onChange={(value) => setReason(value as ManufacturingDowntimeReason | null)}
              error={touched && missing.reason ? t("problem_required") : undefined}
            />
          )}
        </>
      )}
      <SimpleGrid cols={{ base: 1, sm: 2 }}>
        <TextInput
          type="datetime-local"
          label={t("startedAt")}
          value={values.startedAt}
          onChange={(event) => set("startedAt", event.currentTarget.value)}
          error={shown("startedAt")}
        />
        <TextInput
          type="datetime-local"
          label={t("endedAt")}
          value={values.endedAt}
          onChange={(event) => set("endedAt", event.currentTarget.value)}
          error={shown("endedAt")}
        />
      </SimpleGrid>
      <TextInput
        label={t("logQuantity")}
        inputMode="decimal"
        value={values.quantity}
        onChange={(event) => set("quantity", event.currentTarget.value)}
        error={shown("quantity")}
      />
      <Textarea
        label={t("correctionNote")}
        description={t("correctionNoteHint")}
        rows={3}
        maxLength={TIME_LOG_NOTE_MAX}
        value={values.note}
        onChange={(event) => set("note", event.currentTarget.value)}
        error={shown("note")}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={sending} disabled={touched && invalid} onClick={() => void save()}>
          {log ? t("correct") : t("addLog")}
        </Button>
      </Group>
    </Stack>
  );
};

import { ActionIcon, Alert, Button, Group, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ContentSkeleton, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { closuresQueryOptions, createClosure, deleteClosure, MAX_CLOSURES } from "../api/closures";
import { refusedFields } from "../api/request";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

export interface ClosuresPanelProps {
  workCenterId: number;
  /** Whether the caller may add and delete closures: `manufacturing:boms-manage`. */
  canManage: boolean;
}

/**
 * A work center's closures (M9): date ranges it is closed, both days
 * included, with a note — an industrial holiday, a breakdown. At most 100;
 * the end is on or after the start.
 */
export const ClosuresPanel = ({ workCenterId, canManage }: ClosuresPanelProps) => {
  const { t, day } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const closures = useQuery(closuresQueryOptions(workCenterId));
  const [fromDate, setFromDate] = useState("");
  const [toDate, setToDate] = useState("");
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [problem, setProblem] = useState<string>();

  const add = useMutation({
    mutationFn: () => createClosure(workCenterId, { fromDate, toDate, note: note.trim() }),
    onSuccess: async () => {
      setFromDate("");
      setToDate("");
      setNote("");
      notifications.show({ color: "teal", title: t("closureAdded"), message: "" });
      await invalidate();
    },
    onError: (error) => {
      setErrors(refusedFields(error));
      setProblem(refusalMessage(t, error));
    },
  });
  const remove = useMutation({
    mutationFn: (closureId: number) => deleteClosure(workCenterId, closureId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("closureDeleted"), message: "" });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDelete"), message: refusalMessage(t, error) }),
  });

  const submit = () => {
    const found: Record<string, string> = {};
    if (!fromDate) found.fromDate = t("required");
    if (!toDate) found.toDate = t("required");
    if (fromDate && toDate && toDate < fromDate) found.toDate = t("closureEndBeforeStart");
    setErrors(found);
    setProblem(undefined);
    if (Object.keys(found).length === 0) add.mutate();
  };

  const items = closures.data ?? [];

  return (
    <PageSection title={t("closures")}>
      <Stack gap="md" data-assist="manufacturing.work-center.closures">
        <Text size="sm" c="dimmed">
          {t("closuresDescription")}
        </Text>
        {closures.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {closures.error.message}
          </Alert>
        ) : closures.isPending ? (
          <ContentSkeleton rows={2} />
        ) : items.length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noClosures")}
          </Text>
        ) : (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("closureFrom")}</Table.Th>
                <Table.Th>{t("closureTo")}</Table.Th>
                <Table.Th>{t("note")}</Table.Th>
                {canManage && <Table.Th />}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((closure) => (
                <Table.Tr key={closure.id}>
                  <Table.Td>{day(closure.fromDate)}</Table.Td>
                  <Table.Td>{day(closure.toDate)}</Table.Td>
                  <Table.Td>{closure.note}</Table.Td>
                  {canManage && (
                    <Table.Td>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("deleteClosure", { from: day(closure.fromDate) })}
                        loading={remove.isPending && remove.variables === closure.id}
                        onClick={() => remove.mutate(closure.id)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Table.Td>
                  )}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {canManage && items.length < MAX_CLOSURES && (
          <Group align="flex-start" wrap="wrap">
            <TextInput
              type="date"
              label={t("closureFrom")}
              value={fromDate}
              onChange={(event) => setFromDate(event.currentTarget.value)}
              error={errors.fromDate}
            />
            <TextInput
              type="date"
              label={t("closureTo")}
              value={toDate}
              onChange={(event) => setToDate(event.currentTarget.value)}
              error={errors.toDate}
            />
            <TextInput
              label={t("note")}
              value={note}
              maxLength={500}
              onChange={(event) => setNote(event.currentTarget.value)}
              error={errors.note}
              style={{ flex: 1, minWidth: 200 }}
            />
            <Button mt={24} leftSection={<IconPlus size={16} />} loading={add.isPending} onClick={submit}>
              {t("addClosure")}
            </Button>
          </Group>
        )}
        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {problem}
          </Alert>
        )}
      </Stack>
    </PageSection>
  );
};

import { Alert, Button, Card, Group, SimpleGrid, Stack, Table, Text, TextInput, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type AccountingMeta, lockQueryOptions, putLock } from "../api/accounting";
import { ACCOUNTING_QUERY_KEY, refusedFields } from "../api/request";
import { BooksGate } from "../components/books-gate";
import { refusalMessage, useAccountingFormat } from "../lib/format";
import { monthEndOf } from "../lib/lock";

/**
 * The books' settings: the fiscal settings, fixed at setup, and the lock
 * (A8, R10) — moved forward to a month end, or back with a reason, every move
 * kept in its history.
 */
export const SettingsPage = () => {
  const { t } = useAccountingFormat();
  return (
    <BooksGate>
      {(meta) => (
        <Stack gap="lg">
          <PageHeader title={t("navSettings")} description={t("settingsDescription")} />
          <FiscalSettings meta={meta} />
          <Lock canClose={meta.canClose} />
        </Stack>
      )}
    </BooksGate>
  );
};

const FiscalSettings = ({ meta }: { meta: AccountingMeta }) => {
  const { t, day } = useAccountingFormat();
  const s = meta.settings;
  if (!s) return null;
  return (
    <Card withBorder padding="md">
      <Title order={4} mb="xs">
        {t("fiscalSettings")}
      </Title>
      <SimpleGrid cols={{ base: 1, sm: 4 }}>
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            {t("booksStart")}
          </Text>
          <Text>{day(s.booksStart)}</Text>
        </Stack>
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            {t("fiscalYearStartMonth")}
          </Text>
          <Text>{t(`month_${s.fiscalYearStartMonth}`)}</Text>
        </Stack>
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            {t("vatTerm")}
          </Text>
          <Text>{t(`vatTerm_${s.vatTerm}`)}</Text>
        </Stack>
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            {t("chartLanguage")}
          </Text>
          <Text>{t(`language_${s.chartLanguage}`)}</Text>
        </Stack>
      </SimpleGrid>
      <Text size="sm" c="dimmed" mt="sm">
        {t("fiscalSettingsFixed")}
      </Text>
    </Card>
  );
};

const Lock = ({ canClose }: { canClose: boolean }) => {
  const { t, day, stamp } = useAccountingFormat();
  const lock = useQuery(lockQueryOptions());
  const queryClient = useQueryClient();
  const [month, setMonth] = useState("");
  const [reason, setReason] = useState("");
  const move = useMutation({
    mutationFn: (lockedThrough: string | null) => putLock({ lockedThrough, reason: reason.trim() || null }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ACCOUNTING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("lockMoved") });
      setMonth("");
      setReason("");
    },
  });
  if (lock.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {lock.error.message}
      </Alert>
    );
  if (lock.isPending) return <ContentSkeleton rows={3} />;
  const current = lock.data.lockedThrough;
  const target = monthEndOf(month);
  const back = current !== null && target !== null && target < current;
  const fields = refusedFields(move.error);
  return (
    <Card withBorder padding="md" data-assist="accounting.settings.lock">
      <Group gap="xs" mb="xs">
        <IconLock size={18} />
        <Title order={4}>{t("lockDate")}</Title>
      </Group>
      <Text mb="sm">{current ? t("lockedThrough", { date: day(current) }) : t("notLockedHint")}</Text>
      {canClose && (
        <Stack gap="sm">
          <Group align="flex-end">
            <TextInput
              type="month"
              label={t("lockThrough")}
              description={t("lockThroughHint")}
              value={month}
              onChange={(e) => setMonth(e.currentTarget.value)}
              error={fields.lockedThrough}
            />
            <Button
              onClick={() => move.mutate(target)}
              loading={move.isPending}
              disabled={target === null || (back && !reason.trim())}
            >
              {back ? t("moveLockBack") : t("moveLock")}
            </Button>
            {current && (
              <Button variant="subtle" color="red" onClick={() => move.mutate(null)} disabled={!reason.trim()}>
                {t("liftLock")}
              </Button>
            )}
          </Group>
          {(back || current) && (
            <TextInput
              label={t("reason")}
              description={t("lockReasonHint")}
              value={reason}
              onChange={(e) => setReason(e.currentTarget.value)}
              error={fields.reason}
            />
          )}
          {move.error && Object.keys(fields).length === 0 && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
              {refusalMessage(t, move.error)}
            </Alert>
          )}
        </Stack>
      )}
      {lock.data.events.length > 0 && (
        <Table mt="md">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("changedAt")}</Table.Th>
              <Table.Th>{t("from")}</Table.Th>
              <Table.Th>{t("to")}</Table.Th>
              <Table.Th>{t("reason")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {lock.data.events.map((e) => (
              <Table.Tr key={e.changedAt}>
                <Table.Td>{stamp(e.changedAt)}</Table.Td>
                <Table.Td>{e.lockedFrom ? day(e.lockedFrom) : t("notLocked")}</Table.Td>
                <Table.Td>{e.lockedTo ? day(e.lockedTo) : t("notLocked")}</Table.Td>
                <Table.Td>{e.reason}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Card>
  );
};

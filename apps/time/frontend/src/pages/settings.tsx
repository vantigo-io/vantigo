import { ActionIcon, Alert, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { deletePersonRate, type PersonRate, personRatesQueryOptions } from "../api/rates";
import { timeSettingsQueryOptions, updateTimeSettings } from "../api/settings";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { RateFormModal, type RateModalState } from "./-rate-form-modal";

/**
 * The time settings (design §8, `time:manage`): the period lock, and the
 * person rate cards §4.3 prices time by. Both are the whole installation's,
 * so the page is one card each rather than a per-person view.
 */
export const SettingsPage = () => {
  const { t } = useI18n("time");
  return (
    <Stack gap="lg">
      <PageHeader title={t("settings")} description={t("settingsDescription")} />
      <LockCard />
      <RatesCard />
    </Stack>
  );
};

/**
 * The period lock (D9). Changing it closes or opens days for everyone, so the
 * field never saves on its own: the date is typed into a draft, and saving it
 * first says in words what the new lock does. A date is also typed one
 * character at a time — half of "Oct 1, 2026" still parses — so nothing may
 * hang off the field's own change.
 */
const LockCard = () => {
  const { t, formatters } = useI18n("time");
  const queryClient = useQueryClient();
  const { data, isPending, isError, error } = useQuery(timeSettingsQueryOptions());
  // Undefined until the caller touches the field: until then the saved date is shown.
  const [draft, setDraft] = useState<string | null | undefined>(undefined);
  const saved = data?.lockedBefore ?? null;
  const lockedBefore = draft === undefined ? saved : draft;
  const changed = lockedBefore !== saved;

  const save = useMutation({
    mutationFn: (lockedBefore: string | null) => updateTimeSettings({ lockedBefore }),
    onSuccess: async (settings) => {
      notifications.show({
        color: "teal",
        title: t("lockSaved"),
        message: settings.lockedBefore
          ? formatters.formatDate(settings.lockedBefore, { dateStyle: "medium", timeZone: "UTC" })
          : t("lockClearConfirm"),
      });
      setDraft(undefined);
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({
        color: "red",
        title: t("couldNotSaveLock"),
        message: refusalMessage(failure, "lockedBefore"),
      }),
  });

  const confirmChange = () =>
    modals.openConfirmModal({
      title: t("lockChangeTitle"),
      children: (
        <Text size="sm">
          {lockedBefore
            ? t("lockSetConfirm", {
                date: formatters.formatDate(lockedBefore, { dateStyle: "medium", timeZone: "UTC" }),
              })
            : t("lockClearConfirm")}
        </Text>
      ),
      labels: { confirm: t("save"), cancel: t("cancel") },
      onConfirm: () => save.mutate(lockedBefore),
    });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Text fw={600} component="h3">
          {t("periodLock")}
        </Text>
        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
            {error.message}
          </Alert>
        )}
        {isPending ? (
          <ContentSkeleton rows={1} rowHeight={48} />
        ) : (
          <Group align="flex-end" gap="sm" wrap="wrap">
            <DateInput
              label={t("lockDate")}
              description={t("lockDateDescription")}
              valueFormat={t("dateInputFormat")}
              clearable
              clearButtonProps={{ "aria-label": t("clearLock") }}
              disabled={save.isPending}
              w={260}
              value={lockedBefore}
              onChange={(value) => setDraft(value || null)}
            />
            <Button disabled={!changed} loading={save.isPending} onClick={confirmChange}>
              {t("saveLock")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/** One person's cards, newest day first — the way a rate chain is read backwards. */
interface RateGroup {
  userId: string;
  displayName: string;
  rates: PersonRate[];
}

const groupByPerson = (rates: PersonRate[]): RateGroup[] => {
  const groups = new Map<string, RateGroup>();
  for (const rate of rates) {
    const group = groups.get(rate.userId) ?? { userId: rate.userId, displayName: rate.displayName, rates: [] };
    group.rates.push(rate);
    groups.set(rate.userId, group);
  }
  return [...groups.values()]
    .map((group) => ({ ...group, rates: [...group.rates].sort((a, b) => b.validFrom.localeCompare(a.validFrom)) }))
    .sort((a, b) => a.displayName.localeCompare(b.displayName));
};

/**
 * The rate cards, grouped by the people the cards themselves name. Who a new
 * card may be *given* to is a different question, and the form's own picker
 * asks the API's user search — so somebody with no hours yet can be priced
 * before their first entry.
 */
const RatesCard = () => {
  const { t } = useI18n("time");
  const { data, isPending, isError, error } = useQuery(personRatesQueryOptions());
  const [modal, setModal] = useState<RateModalState | null>(null);

  const groups = groupByPerson(data ?? []);

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Stack gap={2}>
            <Text fw={600} component="h3">
              {t("rates")}
            </Text>
            <Text size="sm" c="dimmed">
              {t("ratesDescription")}
            </Text>
          </Stack>
          <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModal({ mode: "create" })}>
            {t("addRate")}
          </Button>
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadRates")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={48} />}
        {data && groups.length === 0 && (
          <EmptyState title={t("noRates")} description={t("noRatesDescription")} size="sm" />
        )}

        {groups.map((group) => (
          <PersonRates key={group.userId} group={group} onEdit={setModal} />
        ))}
      </Stack>

      <RateFormModal state={modal} onClose={() => setModal(null)} />
    </Card>
  );
};

const PersonRates = ({ group, onEdit }: { group: RateGroup; onEdit: (state: RateModalState) => void }) => {
  const { t } = useI18n("time");
  return (
    <Stack gap={4} data-rates={group.userId}>
      <Group justify="space-between" wrap="wrap">
        <Text size="sm" fw={600}>
          {group.displayName}
        </Text>
        <Button
          size="compact-xs"
          variant="subtle"
          leftSection={<IconPlus size={12} />}
          onClick={() => onEdit({ mode: "create", userId: group.userId })}
        >
          {t("addRateForPerson", { person: group.displayName })}
        </Button>
      </Group>
      <Table verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("validFrom")}</Table.Th>
            <Table.Th ta="right">{t("billRate")}</Table.Th>
            <Table.Th ta="right">{t("costRate")}</Table.Th>
            <Table.Th>{t("currency")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {group.rates.map((rate) => (
            <RateRow key={rate.id} rate={rate} onEdit={onEdit} />
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  );
};

const RateRow = ({ rate, onEdit }: { rate: PersonRate; onEdit: (state: RateModalState) => void }) => {
  const { t, formatters } = useI18n("time");
  const queryClient = useQueryClient();
  const validFrom = formatters.formatDate(rate.validFrom, { dateStyle: "medium", timeZone: "UTC" });
  const money = (value: number | null | undefined) =>
    value === null || value === undefined ? t("notAvailable") : formatters.formatCurrency(value, rate.currency);

  const remove = useMutation({
    mutationFn: () => deletePersonRate(rate.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("rateDeleted"), message: `${rate.displayName} · ${validFrom}` });
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({
        color: "red",
        title: t("couldNotDeleteRate"),
        message: refusalMessage(failure, "validFrom"),
      }),
  });

  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("deleteRateTitle"),
      children: <Text size="sm">{t("deleteRateConfirm", { date: validFrom })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  return (
    <Table.Tr>
      <Table.Td>{validFrom}</Table.Td>
      <Table.Td ta="right">{money(rate.billRate)}</Table.Td>
      <Table.Td ta="right">{money(rate.costRate)}</Table.Td>
      <Table.Td>{rate.currency}</Table.Td>
      <Table.Td>
        <Group gap={4} justify="flex-end" wrap="nowrap">
          <ActionIcon variant="subtle" aria-label={t("editRate")} onClick={() => onEdit({ mode: "edit", rate })}>
            <IconPencil size={16} />
          </ActionIcon>
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={t("deleteRate")}
            loading={remove.isPending}
            onClick={confirmRemove}
          >
            <IconTrash size={16} />
          </ActionIcon>
        </Group>
      </Table.Td>
    </Table.Tr>
  );
};

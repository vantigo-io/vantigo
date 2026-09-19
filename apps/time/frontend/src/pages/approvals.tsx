import { ActionIcon, Alert, Badge, Button, Card, Checkbox, Group, Pagination, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconChecks, IconChevronDown, IconChevronRight, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { approvalsQueryOptions, approveTimeEntries, type TimeApprovalGroup } from "../api/approvals";
import type { TimeEntry } from "../api/entries";
import type { ApiError } from "../api/request";
import { RefusalList } from "../components/refusal-list";
import "../i18n";
import { useHoursFormat } from "../lib/hours";
import { rowLabel } from "../lib/rows";
import { RejectModal } from "./-reject-modal";

/** The page's URL search params, which the host route validates (Task 7). */
export interface ApprovalsSearch {
  page?: number;
}

/** A group's identity in the queue: one person's one week. */
const groupKey = (group: TimeApprovalGroup) => `${group.userId}/${group.weekStart}`;

/**
 * The approval queue (design §8, §4.2): everything other people submitted
 * that this caller may approve, grouped by person and week, oldest week
 * first. A group opens to its entries; approving and rejecting work on one
 * entry, on a whole group, or on a selection picked across groups. Every
 * batch is all or nothing, so a refusal names each id it turned down and
 * nothing changes.
 *
 * Unapproving is not here: an approved entry is no longer in the queue, and
 * the action lives on the entry in the day view instead.
 */
export const ApprovalsPage = () => {
  const { t } = useI18n("time");
  const { page = 1 } = useSearch({ strict: false }) as ApprovalsSearch;
  const navigate = useNavigate() as (options: unknown) => void;

  const { data, isPending, isError, error } = useQuery(approvalsQueryOptions(page));
  const forbidden = (error as ApiError | null)?.status === 403;
  // Ids in the order they were picked, so the request reads the way the page does.
  const [selected, setSelected] = useState<number[]>([]);
  const [rejecting, setRejecting] = useState<number[] | null>(null);

  // A selection belongs to the page it was picked on: turning the queue starts
  // a new one. State adjusted during render from the previous render's value,
  // the way React documents, rather than an effect that would flash the old bar.
  const [pageShown, setPageShown] = useState(page);
  if (pageShown !== page) {
    setPageShown(page);
    setSelected([]);
  }

  const toggle = (ids: number[], on: boolean) =>
    setSelected((current) =>
      on ? [...current, ...ids.filter((id) => !current.includes(id))] : current.filter((id) => !ids.includes(id)),
    );

  const groups = data?.data ?? [];
  // The batch is what is picked, still listed, and still the caller's to
  // approve. An entry approved on its own, one the queue has moved on from,
  // and one whose capability has since said no are all out of reach — and the
  // server is all or nothing, so a single unreachable id would refuse the lot.
  // Only approvable entries offer a checkbox at all; this is the second lock.
  const approvable = new Set(
    groups.flatMap((group) => group.entries.filter((entry) => entry.capabilities.canApprove).map((entry) => entry.id)),
  );
  const picked = selected.filter((id) => approvable.has(id));

  return (
    <Stack gap="lg">
      <PageHeader title={t("approvals")} description={t("approvalsDescription")} />

      {isError && !forbidden && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadApprovals")}>
          {error.message}
        </Alert>
      )}
      {isPending && <ContentSkeleton rows={3} rowHeight={72} />}

      {forbidden && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState icon={IconLock} title={t("approvesNothing")} description={t("approvesNothingDescription")} />
        </Card>
      )}

      {data && groups.length === 0 && (
        <Card withBorder padding="lg" radius="md">
          <EmptyState icon={IconChecks} title={t("nothingToApprove")} description={t("nothingToApproveDescription")} />
        </Card>
      )}

      {picked.length > 0 && (
        <BatchBar
          ids={picked}
          onReject={() => setRejecting(picked)}
          onClear={() => setSelected([])}
          onApproved={() => setSelected([])}
        />
      )}

      {groups.map((group) => (
        <GroupCard key={groupKey(group)} group={group} selected={picked} onToggle={toggle} onReject={setRejecting} />
      ))}

      {data && data.pagination.totalPages > 1 && (
        <Group justify="center">
          <Pagination
            total={data.pagination.totalPages}
            value={page}
            onChange={(next) => navigate({ search: { page: next } })}
          />
        </Group>
      )}

      <RejectModal
        ids={rejecting}
        onClose={() => setRejecting(null)}
        onRejected={() => {
          setRejecting(null);
          setSelected([]);
        }}
      />
    </Stack>
  );
};

/** Approves a batch, telling the caller every id a refusal named. */
const useApprove = (onApproved: () => void) => {
  const { t } = useI18n("time");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ids: number[]) => approveTimeEntries(ids),
    onSuccess: async (entries) => {
      notifications.show({
        color: "teal",
        title: t("entriesApproved"),
        message: t(entries.length === 1 ? "entryChanged" : "entriesChanged", { count: entries.length }),
      });
      onApproved();
      await queryClient.invalidateQueries({ queryKey: ["time"] });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotApprove"), message: <RefusalList error={failure} /> }),
  });
};

interface BatchBarProps {
  ids: number[];
  onReject: () => void;
  onClear: () => void;
  onApproved: () => void;
}

const BatchBar = ({ ids, onReject, onClear, onApproved }: BatchBarProps) => {
  const { t } = useI18n("time");
  const approve = useApprove(onApproved);
  return (
    <Card withBorder padding="sm" radius="md" data-testid="approval-selection">
      <Group justify="space-between" wrap="wrap">
        <Button variant="subtle" size="xs" onClick={onClear}>
          {t("clearSelection")}
        </Button>
        <Group gap="xs">
          <Button size="xs" color="red" variant="light" onClick={onReject}>
            {t("rejectSelected", { count: ids.length })}
          </Button>
          <Button size="xs" loading={approve.isPending} onClick={() => approve.mutate(ids)}>
            {t("approveSelected", { count: ids.length })}
          </Button>
        </Group>
      </Group>
    </Card>
  );
};

interface GroupCardProps {
  group: TimeApprovalGroup;
  selected: number[];
  onToggle: (ids: number[], on: boolean) => void;
  onReject: (ids: number[]) => void;
}

const GroupCard = ({ group, selected, onToggle, onReject }: GroupCardProps) => {
  const { t, formatters } = useI18n("time");
  const hours = useHoursFormat();
  const [open, setOpen] = useState(false);
  const approve = useApprove(() => undefined);

  // A group acts on, and selects, the entries this caller may act on. The queue
  // only lists what they approve for, but an entry can fall out of reach
  // between the read and the click — the lock moving, say — so the capability
  // decides, and an entry it says no to is not selectable at all.
  const approvable = group.entries.filter((entry) => entry.capabilities.canApprove).map((entry) => entry.id);
  const picked = approvable.filter((id) => selected.includes(id));
  const week = formatters.formatDate(group.weekStart, { dateStyle: "medium", timeZone: "UTC" });
  const panelId = `approval-entries-${groupKey(group)}`;

  return (
    <Card withBorder padding="md" radius="md" data-group={groupKey(group)}>
      <Stack gap="sm">
        <Group justify="space-between" wrap="wrap">
          <Group gap="sm" wrap="nowrap">
            {approvable.length > 0 && (
              <Checkbox
                aria-label={t("selectWeek", { person: group.displayName })}
                checked={picked.length === approvable.length}
                indeterminate={picked.length > 0 && picked.length < approvable.length}
                onChange={(event) => onToggle(approvable, event.currentTarget.checked)}
              />
            )}
            <ActionIcon
              variant="subtle"
              aria-label={open ? t("hideEntries") : t("showEntries")}
              aria-expanded={open}
              aria-controls={panelId}
              onClick={() => setOpen((current) => !current)}
            >
              {open ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
            </ActionIcon>
            <Stack gap={0}>
              <Text fw={600}>{group.displayName}</Text>
              <Text size="xs" c="dimmed">
                {t("weekOf", { week })}
              </Text>
            </Stack>
          </Group>
          <Group gap="xs" wrap="nowrap">
            <Text fw={700}>{t("hoursShort", { hours: hours.display(group.hours) })}</Text>
            {approvable.length > 0 && (
              <>
                <Button size="xs" color="red" variant="light" onClick={() => onReject(approvable)}>
                  {t("reject")}
                </Button>
                <Button size="xs" loading={approve.isPending} onClick={() => approve.mutate(approvable)}>
                  {t("approve")}
                </Button>
              </>
            )}
          </Group>
        </Group>

        {open && (
          <div id={panelId}>
            <EntryTable entries={group.entries} selected={selected} onToggle={onToggle} onReject={onReject} />
          </div>
        )}
      </Stack>
    </Card>
  );
};

interface EntryTableProps {
  entries: TimeEntry[];
  selected: number[];
  onToggle: (ids: number[], on: boolean) => void;
  onReject: (ids: number[]) => void;
}

const EntryTable = ({ entries, selected, onToggle, onReject }: EntryTableProps) => {
  const { t } = useI18n("time");
  return (
    <Table.ScrollContainer minWidth={760}>
      <Table verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            <Table.Th />
            <Table.Th>{t("date")}</Table.Th>
            <Table.Th>{t("trackable")}</Table.Th>
            <Table.Th ta="right">{t("hours")}</Table.Th>
            <Table.Th ta="right">{t("billAmount")}</Table.Th>
            <Table.Th>{t("note")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {entries.map((entry) => (
            <EntryRow
              key={entry.id}
              entry={entry}
              selected={selected.includes(entry.id)}
              onToggle={onToggle}
              onReject={onReject}
            />
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

interface EntryRowProps {
  entry: TimeEntry;
  selected: boolean;
  onToggle: (ids: number[], on: boolean) => void;
  onReject: (ids: number[]) => void;
}

const EntryRow = ({ entry, selected, onToggle, onReject }: EntryRowProps) => {
  const { t, formatters } = useI18n("time");
  const hours = useHoursFormat();
  const approve = useApprove(() => undefined);
  const label = rowLabel(entry);
  const date = formatters.formatDate(entry.entryDate, { dateStyle: "medium", timeZone: "UTC" });

  // The bill rate is on the entry only for a caller who may see the project's
  // money (D8); what it bills is that rate over the entry's own hours.
  const rate = entry.billing?.billRate;
  const amount =
    rate === undefined || rate === null
      ? undefined
      : entry.billing?.currency
        ? formatters.formatCurrency(rate * entry.hours, entry.billing.currency)
        : formatters.formatNumber(rate * entry.hours, { maximumFractionDigits: 2 });

  return (
    <Table.Tr>
      <Table.Td>
        {/* Nothing the caller may not approve goes into a batch: an id they
            cannot act on would refuse the whole all-or-nothing request. */}
        {entry.capabilities.canApprove && (
          <Checkbox
            aria-label={t("selectEntry", { trackable: label, date })}
            checked={selected}
            onChange={(event) => onToggle([entry.id], event.currentTarget.checked)}
          />
        )}
      </Table.Td>
      <Table.Td>{date}</Table.Td>
      <Table.Td>
        <Stack gap={0}>
          <Text size="sm" fw={600}>
            {label}
          </Text>
          <Text size="xs" c="dimmed">
            {entry.projectName}
          </Text>
        </Stack>
      </Table.Td>
      <Table.Td ta="right">{hours.display(entry.hours)}</Table.Td>
      <Table.Td ta="right">
        {entry.billable ? (
          (amount ?? t("notAvailable"))
        ) : (
          <Badge variant="light" color="gray">
            {t("notBillable")}
          </Badge>
        )}
      </Table.Td>
      <Table.Td>
        <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
          {entry.note}
        </Text>
      </Table.Td>
      <Table.Td>
        {entry.capabilities.canApprove && (
          <Group gap={4} justify="flex-end" wrap="nowrap">
            <Button size="compact-xs" color="red" variant="subtle" onClick={() => onReject([entry.id])}>
              {t("reject")}
            </Button>
            <Button
              size="compact-xs"
              variant="light"
              loading={approve.isPending}
              onClick={() => approve.mutate([entry.id])}
            >
              {t("approve")}
            </Button>
          </Group>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

import {
  Accordion,
  ActionIcon,
  Badge,
  Button,
  Card,
  Center,
  Drawer,
  Group,
  Loader,
  Menu,
  Modal,
  MultiSelect,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  ThemeIcon,
  Timeline,
} from "@mantine/core";
import { DateInput, DatePickerInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import {
  IconCalendarEvent,
  IconClock,
  IconDots,
  IconEdit,
  IconExternalLink,
  IconHistory,
  IconPlus,
  IconTrash,
  IconWand,
} from "@tabler/icons-react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  createTimelineEntry,
  defaultTimelineFilters,
  deleteTimelineEntry,
  fetchTimeline,
  normalizeTimelineFilters,
  type TimelineEntry,
  type TimelineFilters,
  type TimelineInput,
  timelineRevisionsQueryOptions,
  updateTimelineEntry,
} from "../api/timeline";
import "../i18n";

const manualTypes = [
  "registry.change",
  "interaction.call",
  "interaction.meeting",
  "interaction.email",
  "note",
  "other",
];
const typeKey: Record<string, string> = {
  "registry.change": "registryChange",
  "interaction.call": "call",
  "interaction.meeting": "meeting",
  "interaction.email": "emailEvent",
  note: "note",
  other: "other",
  "customer.created": "customerCreatedEvent",
  "customer.updated": "customerUpdatedEvent",
  "customer.contact_attached": "contactLinked",
  "customer.contact_relationship_updated": "contactRelationshipUpdated",
  "customer.contact_detached": "contactUnlinked",
  "customer.contact_removed": "contactRemoved",
};
const iconFor = (type: string) =>
  type.startsWith("interaction.") ? IconCalendarEvent : type === "note" ? IconEdit : IconWand;
const utcToday = () => new Date().toISOString().slice(0, 10);
const contactReference = (payload: unknown) => {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as Record<string, unknown>;
  const contact = record.contact;
  if (contact && typeof contact === "object" && typeof (contact as Record<string, unknown>).displayName === "string")
    return (contact as Record<string, string>).displayName;
  return typeof record.displayName === "string"
    ? record.displayName
    : typeof record.contactName === "string"
      ? record.contactName
      : typeof record.name === "string"
        ? record.name
        : null;
};
const displayValue = (value: unknown): string | null => {
  if (value == null) return null;
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    return displayValue(record.displayName ?? record.name ?? record.value ?? record.id);
  }
  return null;
};
const payloadDetails = (payload: unknown, t: (key: string, options?: Record<string, unknown>) => string) => {
  if (!payload || typeof payload !== "object") return null;
  const changes = (payload as Record<string, unknown>).changes;
  if (!changes || typeof changes !== "object") return null;
  const details: string[] = [];
  for (const [field, raw] of Object.entries(changes as Record<string, unknown>)) {
    const value = raw && typeof raw === "object" ? (raw as Record<string, unknown>) : { new: raw };
    const before = value.before ?? value.old ?? value.previous;
    const after = value.after ?? value.new ?? value.current;
    if (field === "legalIdentity" || field === "identity") {
      const identityFields = ["name", "id", "country", "type", "source"] as const;
      const oldRecord = before && typeof before === "object" ? (before as Record<string, unknown>) : {};
      const newRecord = after && typeof after === "object" ? (after as Record<string, unknown>) : {};
      const changed = identityFields
        .filter((key) => displayValue(oldRecord[key]) !== displayValue(newRecord[key]))
        .map((key) => {
          const label =
            key === "name"
              ? t("legalName")
              : key === "id"
                ? t("identityId")
                : key === "country"
                  ? t("country")
                  : key === "type"
                    ? t("identityType")
                    : t("sourceField");
          const oldValue = displayValue(oldRecord[key]);
          const newValue = displayValue(newRecord[key]);
          return oldValue == null && newValue != null
            ? t("fieldAddedShort", { field: label, value: newValue })
            : newValue == null && oldValue != null
              ? t("fieldRemovedShort", { field: label, value: oldValue })
              : t("fieldChanged", { field: label, oldValue, newValue });
        });
      if (changed.length)
        details.push(
          t(before == null ? "identityAdded" : after == null ? "identityRemoved" : "identityUpdated", {
            details: changed.join(", "),
          }),
        );
      continue;
    }
    const oldValue = displayValue(before);
    const newValue = displayValue(after);
    if (oldValue === newValue) continue;
    const label =
      field === "customerName"
        ? t("customerNameField")
        : field.replace(/([A-Z])/g, " $1").replace(/^./, (letter) => letter.toUpperCase());
    if (oldValue == null && newValue != null) {
      details.push(t("fieldAdded", { field: label, value: newValue }));
    } else if (newValue == null && oldValue != null) {
      details.push(t("fieldRemoved", { field: label, value: oldValue }));
    } else if (oldValue != null && newValue != null) {
      details.push(t("fieldUpdated", { field: label, oldValue, newValue }));
    }
  }
  return details.filter(Boolean).join(" · ") || null;
};
const dateValue = (value: Date | string | null) =>
  value
    ? typeof value === "string"
      ? value.slice(0, 10)
      : `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}`
    : null;

export const CustomerTimeline = ({ customerId }: { customerId: number }) => {
  const { t, formatters } = useI18n("customers");
  const manualTypeOptions = manualTypes.map((value) => ({ value, label: t(typeKey[value]) }));
  const eventTypeOptions = [
    ...manualTypeOptions,
    ...Object.keys(typeKey)
      .filter((value) => !manualTypes.includes(value))
      .map((value) => ({ value, label: t(typeKey[value]) })),
  ];
  const typeLabel = (type: string) => t(typeKey[type] ?? "timelineEvent");
  const formatDateOnly = (date: string) =>
    formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" });
  const formatMoment = (date: string, time?: string | null) =>
    `${formatDateOnly(date)}${time ? ` · ${formatters.formatDate(time, { timeStyle: "short", timeZone: "UTC" })} UTC` : ""}`;
  const client = useQueryClient();
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<TimelineEntry | null>(null);
  const [revisions, setRevisions] = useState<TimelineEntry | null>(null);
  const [deleting, setDeleting] = useState<TimelineEntry | null>(null);
  const [filters, setFilters] = useState<TimelineFilters>(defaultTimelineFilters);
  const [draft, setDraft] = useState(filters);
  const [filterOpen, setFilterOpen] = useState(false);
  const small = useMediaQuery("(max-width: 48em)");
  const activeCount =
    (filters.provenance !== "all" ? 1 : 0) +
    filters.eventTypes.length +
    (filters.occurredFrom || filters.occurredTo ? 1 : 0);
  const feed = useInfiniteQuery({
    queryKey: ["customers", customerId, "timeline", normalizeTimelineFilters(filters)],
    queryFn: ({ pageParam, signal }) => fetchTimeline(customerId, pageParam, signal, filters),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  });
  const entries = feed.data?.pages.flatMap((page) => page.data) ?? [];
  const refresh = () => client.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
  const remove = useMutation({
    mutationFn: (entry: TimelineEntry) => deleteTimelineEntry(customerId, entry),
    onSuccess: () => {
      setDeleting(null);
      refresh();
    },
    onError: (error: Error & { status?: number }) => {
      setDeleting(null);
      refresh();
      notifications.show({
        color: error.status === 409 ? "yellow" : "red",
        title: error.status === 409 ? t("eventChanged") : t("couldNotDeleteEvent"),
        message: error.status === 409 ? t("timelineRefreshed") : error.message,
      });
    },
  });
  const reset = () => {
    setDraft(defaultTimelineFilters);
    setFilters(defaultTimelineFilters);
    setFilterOpen(false);
  };
  const apply = () => {
    setFilters(normalizeTimelineFilters(draft));
    setFilterOpen(false);
  };
  const inputs = (
    <Stack gap="sm">
      <SegmentedControl
        fullWidth
        aria-label={t("sourceLabel")}
        data={[
          { value: "all", label: t("all") },
          { value: "manual", label: t("manual") },
          { value: "generated", label: t("automatic") },
        ]}
        value={draft.provenance}
        onChange={(value) => setDraft({ ...draft, provenance: value as TimelineFilters["provenance"] })}
      />
      <MultiSelect
        label={t("eventTypes")}
        data={eventTypeOptions}
        value={draft.eventTypes}
        onChange={(value) => setDraft({ ...draft, eventTypes: value })}
        searchable
        clearable
      />
      <DatePickerInput
        type="range"
        label={t("occurredOn")}
        value={[
          draft.occurredFrom ? new Date(`${draft.occurredFrom}T00:00:00`) : null,
          draft.occurredTo ? new Date(`${draft.occurredTo}T00:00:00`) : null,
        ]}
        onChange={(value) => setDraft({ ...draft, occurredFrom: dateValue(value[0]), occurredTo: dateValue(value[1]) })}
        valueFormat="YYYY-MM-DD"
        clearable
      />
    </Stack>
  );
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap">
          <Group gap="xs">
            <IconHistory size={18} aria-hidden="true" />
            <Text fw={600} component="h3">
              {t("timeline")}
            </Text>
          </Group>
          <Group gap="xs">
            <Button
              size="xs"
              variant="light"
              leftSection={<IconPlus size={14} />}
              onClick={() => {
                setEditing(null);
                setFormOpen(true);
              }}
            >
              {t("addEvent")}
            </Button>
            {small && (
              <Button
                size="xs"
                variant="default"
                onClick={() => {
                  setDraft(filters);
                  setFilterOpen(true);
                }}
              >
                {t("filters")} {activeCount > 0 && <Badge ml={4}>{formatters.formatNumber(activeCount)}</Badge>}
              </Button>
            )}
          </Group>
        </Group>
        {small ? (
          <Drawer opened={filterOpen} onClose={() => setFilterOpen(false)} title={t("filters")} position="right">
            <Stack>
              {inputs}
              <Group justify="space-between">
                <Button variant="subtle" onClick={reset}>
                  {t("reset")}
                </Button>
                <Button onClick={apply}>{t("apply")}</Button>
              </Group>
            </Stack>
          </Drawer>
        ) : (
          <Group align="end" wrap="wrap">
            <SegmentedControl
              aria-label={t("sourceLabel")}
              data={[
                { value: "all", label: t("all") },
                { value: "manual", label: t("manual") },
                { value: "generated", label: t("automatic") },
              ]}
              value={draft.provenance}
              onChange={(value) => {
                const next = { ...draft, provenance: value as TimelineFilters["provenance"] };
                setDraft(next);
                setFilters(normalizeTimelineFilters(next));
              }}
            />
            <MultiSelect
              w={260}
              label={t("eventTypes")}
              data={eventTypeOptions}
              value={draft.eventTypes}
              onChange={(value) => {
                const next = { ...draft, eventTypes: value };
                setDraft(next);
                setFilters(normalizeTimelineFilters(next));
              }}
              searchable
              clearable
            />
            <DatePickerInput
              type="range"
              label={t("occurredOn")}
              value={[
                draft.occurredFrom ? new Date(`${draft.occurredFrom}T00:00:00`) : null,
                draft.occurredTo ? new Date(`${draft.occurredTo}T00:00:00`) : null,
              ]}
              onChange={(value) => {
                const next = { ...draft, occurredFrom: dateValue(value[0]), occurredTo: dateValue(value[1]) };
                setDraft(next);
                setFilters(normalizeTimelineFilters(next));
              }}
              valueFormat="YYYY-MM-DD"
              clearable
            />
            <Badge variant="light">{t("active", { count: activeCount })}</Badge>
            <Button variant="subtle" onClick={reset}>
              {t("reset")}
            </Button>
          </Group>
        )}
        {feed.isPending ? (
          <Center py="xl">
            <Loader size="sm" />
          </Center>
        ) : feed.isError ? (
          <Stack align="center" py="md">
            <Text c="red">{t("couldNotLoadTimeline")}</Text>
            <Button variant="light" onClick={() => feed.refetch()}>
              {t("tryAgain")}
            </Button>
          </Stack>
        ) : entries.length === 0 ? (
          <Center py="xl">
            <Text c="dimmed">{activeCount ? t("noEventsMatch") : t("noEventsYet")}</Text>
          </Center>
        ) : (
          <Timeline active={-1} bulletSize={30} lineWidth={2}>
            {entries.map((entry) => {
              const EventIcon = iconFor(entry.eventType);
              const details = payloadDetails(entry.payload, t);
              const contact = contactReference(entry.payload);
              return (
                <Timeline.Item
                  key={entry.id}
                  bullet={
                    <ThemeIcon
                      size={30}
                      radius="xl"
                      variant={entry.provenance === "generated" ? "light" : "filled"}
                      color={entry.provenance === "generated" ? "gray" : "blue"}
                    >
                      <EventIcon size={16} />
                    </ThemeIcon>
                  }
                  title={
                    <Group gap="xs">
                      <Text fw={600}>{typeLabel(entry.eventType)}</Text>
                      <Badge size="xs" variant="light">
                        {entry.provenance === "generated" ? t("automaticEvent") : t("manualEvent")}
                      </Badge>
                    </Group>
                  }
                >
                  <Group justify="space-between" align="flex-start" wrap="wrap">
                    <Stack gap={4} style={{ minWidth: 0, flex: 1 }}>
                      <Text size="sm" c="dimmed">
                        {formatMoment(entry.occurredOn, entry.occurredAt)}
                        {entry.producer ? ` · ${entry.producer}` : ""}
                      </Text>
                      <Text size="sm">{entry.note || entry.summary || t("noAdditionalDetails")}</Text>
                      {details && (
                        <Text size="sm" c="dimmed">
                          {details}
                        </Text>
                      )}
                      {!entry.note && !(contact && entry.summary?.includes(contact)) && contact && (
                        <Text size="sm" c="dimmed">
                          {t("contactReference", { name: contact })}
                        </Text>
                      )}
                      {entry.sourceUrl && (
                        <Button
                          component="a"
                          href={entry.sourceUrl}
                          target="_blank"
                          rel="noreferrer"
                          variant="subtle"
                          size="compact-sm"
                          px={0}
                          leftSection={<IconExternalLink size={14} />}
                        >
                          {t("openSource")}
                        </Button>
                      )}
                    </Stack>
                    {entry.provenance === "manual" && (
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon
                            variant="subtle"
                            aria-label={t("timelineActions", {
                              type: typeLabel(entry.eventType),
                              date: formatDateOnly(entry.occurredOn),
                              id: entry.id,
                            })}
                          >
                            <IconDots size={18} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item
                            leftSection={<IconEdit size={15} />}
                            onClick={() => {
                              setEditing(entry);
                              setFormOpen(true);
                            }}
                          >
                            {t("edit")}
                          </Menu.Item>
                          <Menu.Item leftSection={<IconHistory size={15} />} onClick={() => setRevisions(entry)}>
                            {t("revisionHistory")}
                          </Menu.Item>
                          <Menu.Item
                            color="red"
                            leftSection={<IconTrash size={15} />}
                            onClick={() => setDeleting(entry)}
                          >
                            {t("delete")}
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    )}
                  </Group>
                </Timeline.Item>
              );
            })}
          </Timeline>
        )}
        {feed.hasNextPage && (
          <Button variant="default" loading={feed.isFetchingNextPage} onClick={() => feed.fetchNextPage()}>
            {t("loadMore")}
          </Button>
        )}
      </Stack>
      <TimelineForm
        customerId={customerId}
        entry={editing}
        opened={formOpen}
        onClose={() => setFormOpen(false)}
        onSuccess={() => {
          setFormOpen(false);
          refresh();
        }}
      />
      <RevisionPanel customerId={customerId} entry={revisions} onClose={() => setRevisions(null)} />
      <Modal
        opened={Boolean(deleting)}
        onClose={() => !remove.isPending && setDeleting(null)}
        title={t("deleteTimelineEvent")}
        centered
      >
        <Stack>
          <Text>{t("deleteEventQuestion")}</Text>
          <Group justify="flex-end">
            <Button variant="default" disabled={remove.isPending} onClick={() => setDeleting(null)}>
              {t("cancel")}
            </Button>
            <Button
              color="red"
              loading={remove.isPending}
              disabled={remove.isPending}
              onClick={() => deleting && !remove.isPending && remove.mutate(deleting)}
            >
              {t("deleteEvent")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Card>
  );
};

const TimelineForm = ({
  customerId,
  entry,
  opened,
  onClose,
  onSuccess,
}: {
  customerId: number;
  entry: TimelineEntry | null;
  opened: boolean;
  onClose: () => void;
  onSuccess: () => void;
}) => {
  const { t } = useI18n("customers");
  const form = useForm({
    initialValues: { eventType: "note", occurredOn: utcToday(), occurredAt: "", note: "", sourceUrl: "" },
    validate: {
      eventType: (v) => (!v ? t("typeRequired") : null),
      occurredOn: (v) => (!v ? t("dateRequired") : v > utcToday() ? t("dateFuture") : null),
      note: (v) => (!v.trim() ? t("descriptionRequired") : null),
    },
  });
  useEffect(() => {
    if (!opened) return;
    form.setValues(
      entry
        ? {
            eventType: entry.eventType,
            occurredOn: entry.occurredOn,
            occurredAt: entry.occurredAt ? entry.occurredAt.slice(11, 16) : "",
            note: entry.note ?? "",
            sourceUrl: entry.sourceUrl ?? "",
          }
        : { eventType: "note", occurredOn: utcToday(), occurredAt: "", note: "", sourceUrl: "" },
    );
    form.resetDirty();
    form.clearErrors(); // Form methods are stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened, entry?.id]);
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (input: TimelineInput) =>
      entry
        ? updateTimelineEntry(customerId, entry.id, input, entry.currentRevision)
        : createTimelineEntry(customerId, input),
    onSuccess,
    onError: (error: Error & { status?: number }) => {
      if (error.status === 409) client.invalidateQueries({ queryKey: ["customers", customerId, "timeline"] });
      notifications.show({
        color: "red",
        title: error.status === 409 ? t("thisEventChanged") : t("couldNotSaveEvent"),
        message: error.message,
      });
    },
  });
  const submit = form.onSubmit((values) =>
    mutation.mutate({
      ...values,
      occurredAt: values.occurredAt
        ? new Date(`${values.occurredOn}T${values.occurredAt}:00Z`).toISOString()
        : undefined,
      sourceUrl: values.sourceUrl || undefined,
    }),
  );
  return (
    <Modal opened={opened} onClose={onClose} title={entry ? t("editTimelineEvent") : t("addTimelineEvent")} centered>
      <form onSubmit={submit}>
        <Stack>
          <Select
            label={t("type")}
            data={manualTypes.map((value) => ({ value, label: t(typeKey[value]) }))}
            withAsterisk
            {...form.getInputProps("eventType")}
          />
          <DateInput label={t("date")} valueFormat="YYYY-MM-DD" withAsterisk {...form.getInputProps("occurredOn")} />
          <TextInput
            label={t("timeUtc")}
            type="time"
            leftSection={<IconClock size={16} />}
            {...form.getInputProps("occurredAt")}
          />
          <Textarea label={t("description")} withAsterisk minRows={3} {...form.getInputProps("note")} />
          <TextInput
            label={t("sourceUrl")}
            placeholder={t("sourceUrlPlaceholder")}
            {...form.getInputProps("sourceUrl")}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {entry ? t("saveChanges") : t("addEvent")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};

const RevisionPanel = ({
  customerId,
  entry,
  onClose,
}: {
  customerId: number;
  entry: TimelineEntry | null;
  onClose: () => void;
}) => {
  const { t } = useI18n("customers");
  const query = useQuery({ ...timelineRevisionsQueryOptions(customerId, entry?.id ?? 0), enabled: Boolean(entry) });
  const body = (
    <Stack>
      <Text size="sm" c="dimmed">
        {t("revisionAuditDescription")}
      </Text>
      {query.isPending ? (
        <Loader size="sm" />
      ) : query.isError ? (
        <Text c="red">{t("couldNotLoadRevisions")}</Text>
      ) : (
        <Accordion variant="separated">
          {query.data?.map((revision) => (
            <Accordion.Item key={revision.revision} value={String(revision.revision)}>
              <Accordion.Control>{t("revision", { number: revision.revision })}</Accordion.Control>
              <Accordion.Panel>
                <Stack gap="xs">
                  <Text size="sm">
                    {revision.action} · {revision.actorDisplayName || t("unattributed")}
                  </Text>
                  <Text>{revision.note || t("noDescription")}</Text>
                </Stack>
              </Accordion.Panel>
            </Accordion.Item>
          ))}
        </Accordion>
      )}
    </Stack>
  );
  return (
    <Modal opened={Boolean(entry)} onClose={onClose} title={t("revisionHistory")} centered>
      {body}
    </Modal>
  );
};

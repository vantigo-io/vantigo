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

const manualTypes = [
  { value: "registry.change", label: "Registry change" },
  { value: "interaction.call", label: "Call" },
  { value: "interaction.meeting", label: "Meeting" },
  { value: "interaction.email", label: "Email" },
  { value: "note", label: "Note" },
  { value: "other", label: "Other" },
];
const generatedLabels: Record<string, string> = {
  "customer.created": "Customer created",
  "customer.updated": "Customer updated",
  "customer.contact_attached": "Contact linked",
  "customer.contact_relationship_updated": "Contact relationship updated",
  "customer.contact_detached": "Contact unlinked",
  "customer.contact_removed": "Contact removed",
};
const eventTypeOptions = [
  ...manualTypes,
  ...Object.entries(generatedLabels).map(([value, label]) => ({ value, label })),
];
const typeLabel = (type: string) =>
  manualTypes.find((item) => item.value === type)?.label ?? generatedLabels[type] ?? "Timeline event";
const iconFor = (type: string) =>
  type.startsWith("interaction.") ? IconCalendarEvent : type === "note" ? IconEdit : IconWand;
const utcToday = () => new Date().toISOString().slice(0, 10);
const formatMoment = (date: string, time?: string | null) =>
  `${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${date}T00:00:00Z`))}${time ? ` · ${new Intl.DateTimeFormat(undefined, { timeStyle: "short", timeZone: "UTC" }).format(new Date(time))} UTC` : ""}`;
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
const payloadDetails = (payload: unknown) => {
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
              ? "legal name"
              : key === "id"
                ? "ID"
                : key === "country"
                  ? "country"
                  : key === "type"
                    ? "type"
                    : "source";
          const oldValue = displayValue(oldRecord[key]);
          const newValue = displayValue(newRecord[key]);
          return oldValue == null && newValue != null
            ? `${label} added: ${newValue}`
            : newValue == null && oldValue != null
              ? `${label} removed (was ${oldValue})`
              : `${label}: ${oldValue} → ${newValue}`;
        });
      if (changed.length)
        details.push(
          `Legal identity ${before == null ? "added" : after == null ? "removed" : "updated"}: ${changed.join(", ")}`,
        );
      continue;
    }
    const oldValue = displayValue(before);
    const newValue = displayValue(after);
    if (oldValue === newValue) continue;
    const label = field.replace(/([A-Z])/g, " $1").replace(/^./, (letter) => letter.toUpperCase());
    details.push(
      oldValue == null && newValue != null
        ? `${label} added: ${newValue}`
        : newValue == null && oldValue != null
          ? `${label} removed (was ${oldValue})`
          : oldValue != null && newValue != null
            ? `${label} updated: ${oldValue} → ${newValue}`
            : "",
    );
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
        title: error.status === 409 ? "Event changed" : "Could not delete event",
        message: error.status === 409 ? "The timeline was refreshed; please retry deletion." : error.message,
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
        aria-label="Source"
        data={[
          { value: "all", label: "All" },
          { value: "manual", label: "Manual" },
          { value: "generated", label: "Automatic" },
        ]}
        value={draft.provenance}
        onChange={(value) => setDraft({ ...draft, provenance: value as TimelineFilters["provenance"] })}
      />
      <MultiSelect
        label="Event types"
        data={eventTypeOptions}
        value={draft.eventTypes}
        onChange={(value) => setDraft({ ...draft, eventTypes: value })}
        searchable
        clearable
      />
      <DatePickerInput
        type="range"
        label="Occurred on"
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
              Timeline
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
              Add event
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
                Filters {activeCount > 0 && <Badge ml={4}>{activeCount}</Badge>}
              </Button>
            )}
          </Group>
        </Group>
        {small ? (
          <Drawer opened={filterOpen} onClose={() => setFilterOpen(false)} title="Filters" position="right">
            <Stack>
              {inputs}
              <Group justify="space-between">
                <Button variant="subtle" onClick={reset}>
                  Reset
                </Button>
                <Button onClick={apply}>Apply</Button>
              </Group>
            </Stack>
          </Drawer>
        ) : (
          <Group align="end" wrap="wrap">
            <SegmentedControl
              aria-label="Source"
              data={[
                { value: "all", label: "All" },
                { value: "manual", label: "Manual" },
                { value: "generated", label: "Automatic" },
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
              label="Event types"
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
              label="Occurred on"
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
            <Badge variant="light">{activeCount} active</Badge>
            <Button variant="subtle" onClick={reset}>
              Reset
            </Button>
          </Group>
        )}
        {feed.isPending ? (
          <Center py="xl">
            <Loader size="sm" />
          </Center>
        ) : feed.isError ? (
          <Stack align="center" py="md">
            <Text c="red">Could not load the timeline.</Text>
            <Button variant="light" onClick={() => feed.refetch()}>
              Try again
            </Button>
          </Stack>
        ) : entries.length === 0 ? (
          <Center py="xl">
            <Text c="dimmed">
              {activeCount
                ? "No events match these filters."
                : "No events yet. Add the first moment worth remembering."}
            </Text>
          </Center>
        ) : (
          <Timeline active={-1} bulletSize={30} lineWidth={2}>
            {entries.map((entry) => {
              const EventIcon = iconFor(entry.eventType);
              const details = payloadDetails(entry.payload);
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
                        {entry.provenance === "generated" ? "Automatic" : "Manual"}
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
                      <Text size="sm">{entry.note || entry.summary || "No additional details."}</Text>
                      {details && (
                        <Text size="sm" c="dimmed">
                          {details}
                        </Text>
                      )}
                      {!entry.note && !(contact && entry.summary?.includes(contact)) && contact && (
                        <Text size="sm" c="dimmed">
                          Contact: {contact}
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
                          Open source
                        </Button>
                      )}
                    </Stack>
                    {entry.provenance === "manual" && (
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon
                            variant="subtle"
                            aria-label={`Actions for ${typeLabel(entry.eventType)} on ${entry.occurredOn} (event ${entry.id})`}
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
                            Edit
                          </Menu.Item>
                          <Menu.Item leftSection={<IconHistory size={15} />} onClick={() => setRevisions(entry)}>
                            Revision history
                          </Menu.Item>
                          <Menu.Item
                            color="red"
                            leftSection={<IconTrash size={15} />}
                            onClick={() => setDeleting(entry)}
                          >
                            Delete
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
            Load more
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
        title="Delete timeline event"
        centered
      >
        <Stack>
          <Text>Delete this event? It will be removed from the timeline.</Text>
          <Group justify="flex-end">
            <Button variant="default" disabled={remove.isPending} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button
              color="red"
              loading={remove.isPending}
              disabled={remove.isPending}
              onClick={() => deleting && !remove.isPending && remove.mutate(deleting)}
            >
              Delete event
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
  const form = useForm({
    initialValues: { eventType: "note", occurredOn: utcToday(), occurredAt: "", note: "", sourceUrl: "" },
    validate: {
      eventType: (v) => (!v ? "Type is required" : null),
      occurredOn: (v) => (!v ? "Date is required" : v > utcToday() ? "Date cannot be in the future" : null),
      note: (v) => (!v.trim() ? "Description is required" : null),
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
        title: error.status === 409 ? "This event changed" : "Could not save event",
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
    <Modal opened={opened} onClose={onClose} title={entry ? "Edit timeline event" : "Add timeline event"} centered>
      <form onSubmit={submit}>
        <Stack>
          <Select label="Type" data={manualTypes} withAsterisk {...form.getInputProps("eventType")} />
          <DateInput label="Date" valueFormat="YYYY-MM-DD" withAsterisk {...form.getInputProps("occurredOn")} />
          <TextInput
            label="Time (UTC)"
            type="time"
            leftSection={<IconClock size={16} />}
            {...form.getInputProps("occurredAt")}
          />
          <Textarea label="Description" withAsterisk minRows={3} {...form.getInputProps("note")} />
          <TextInput label="Source URL" placeholder="https://…" {...form.getInputProps("sourceUrl")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={mutation.isPending}>
              {entry ? "Save changes" : "Add event"}
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
  const query = useQuery({ ...timelineRevisionsQueryOptions(customerId, entry?.id ?? 0), enabled: Boolean(entry) });
  const body = (
    <Stack>
      <Text size="sm" c="dimmed">
        Every saved version is preserved for auditability.
      </Text>
      {query.isPending ? (
        <Loader size="sm" />
      ) : query.isError ? (
        <Text c="red">Could not load revisions.</Text>
      ) : (
        <Accordion variant="separated">
          {query.data?.map((revision) => (
            <Accordion.Item key={revision.revision} value={String(revision.revision)}>
              <Accordion.Control>Revision {revision.revision}</Accordion.Control>
              <Accordion.Panel>
                <Stack gap="xs">
                  <Text size="sm">
                    {revision.action} · {revision.actorDisplayName || "Unattributed"}
                  </Text>
                  <Text>{revision.note || "No description"}</Text>
                </Stack>
              </Accordion.Panel>
            </Accordion.Item>
          ))}
        </Accordion>
      )}
    </Stack>
  );
  return (
    <Modal opened={Boolean(entry)} onClose={onClose} title="Revision history" centered>
      {body}
    </Modal>
  );
};

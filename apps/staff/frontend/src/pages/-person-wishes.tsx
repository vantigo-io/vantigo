import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  List,
  Modal,
  MultiSelect,
  NumberInput,
  Radio,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  Title,
  VisuallyHidden,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCheck, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  availabilityQueryOptions,
  competenceCategoriesQueryOptions,
  competenceReviewsQueryOptions,
  competencesQueryOptions,
  interestsQueryOptions,
  markWishesSeen,
  personCompetencesQueryOptions,
  preferencesQueryOptions,
  recordCompetenceReview,
  replaceAvailability,
  replaceInterests,
  replacePreferences,
  type StaffAvailabilityDeclaration,
  type StaffCompetence,
  type StaffCompetenceCategory,
  type StaffPersonInterest,
  type StaffPersonPreference,
  type StaffPreferenceKind,
} from "../api/competences";
import { staffMetaQueryOptions } from "../api/meta";
import type { StaffPerson } from "../api/people";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup, Fields } from "../components/field-group";
import "../i18n";
import { refusalText } from "../lib/absence";
import {
  availabilityKindLabel,
  availabilityKinds,
  competenceName,
  declarationInput,
  declarationsKey,
  endOptions,
  followsServer,
  ISO_WEEKDAYS,
  interestsKey,
  minutesLabel,
  offeredCompetences,
  preferenceKindLabel,
  preferenceKinds,
  remoteLabel,
  remoteShares,
  sameDeclaration,
  startOptions,
  timeOfDayLabel,
  timeOfDayParts,
  travelExtents,
  travelLabel,
  WEIGHTS,
  weekdayLabel,
  weightLabel,
} from "../lib/competences";
import { useStaffFormat } from "../lib/format";

type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * A refused whole-list write in words, one line per field: a refusal keyed
 * at an index (`interests[2].competenceId`) names the row it is about, in
 * the catalog's words when it is coded — the list has no field to put it on.
 */
const listRefusal = (error: Error, t: Translate, nameAt: (index: number) => string | undefined): string[] => {
  if (!(error instanceof ApiValidationError)) return [refusalText(error, t)];
  const code = (error as { code?: string }).code;
  const lines = Object.entries(error.fieldErrors).map(([field, message]) => {
    const said = code ? refusalText(error, t) : message;
    const index = /\[(\d+)\]/.exec(field);
    if (!index) return said;
    const what = nameAt(Number(index[1])) ?? t("wishRow", { number: Number(index[1]) + 1 });
    return t("wishRefusalAt", { what, message: said });
  });
  return lines.length > 0 ? lines : [error.message];
};

/** A row's Seen stamp, or — to whoever stamps it — that it is new since. */
const SeenMark = ({ seenAt, stamps, timeZone }: { seenAt?: string; stamps: boolean; timeZone?: string }) => {
  const { t, day } = useStaffFormat();
  if (seenAt)
    return (
      <Group gap={4} wrap="nowrap">
        <IconCheck size={14} color="var(--mantine-color-teal-6)" aria-hidden />
        <Text size="xs" c="dimmed">
          {t("seenOn", { date: day(seenAt, timeZone) })}
        </Text>
      </Group>
    );
  return stamps ? (
    <Badge size="sm" variant="light" color="blue">
      {t("wishNew")}
    </Badge>
  ) : null;
};

const Banner = ({ lines }: { lines: string[] }) => {
  const { t } = useI18n("staff");
  if (lines.length === 0) return null;
  return (
    <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
      <Stack gap={2}>
        {lines.map((line) => (
          <Text key={line} size="sm">
            {line}
          </Text>
        ))}
      </Stack>
    </Alert>
  );
};

/**
 * A whole-list editor's draft over the server's rows, as the settings forms
 * keep theirs: it follows the server while it says what the server last
 * said, or what the server says now; a draft that says something else is
 * kept — never discarded silently — and `stale` until the person reloads it.
 */
const useServerDraft = <T,>(loaded: T, keyOf: (value: T) => string) => {
  const [seeded, setSeeded] = useState(loaded);
  const [draft, setDraft] = useState(loaded);
  const [stale, setStale] = useState(false);
  const loadedKey = keyOf(loaded);
  if (keyOf(seeded) !== loadedKey) {
    setSeeded(loaded);
    if (followsServer(keyOf(draft), keyOf(seeded), loadedKey)) {
      setDraft(loaded);
      setStale(false);
    } else setStale(true);
  }
  return {
    draft,
    setDraft,
    dirty: keyOf(draft) !== keyOf(seeded),
    // A draft brought back to what the server says is no longer at odds with it.
    stale: stale && keyOf(draft) !== loadedKey,
    reload: () => {
      setDraft(seeded);
      setStale(false);
    },
  };
};

/** The rows changed elsewhere while the person had a draft: theirs is kept until they reload. */
const ChangedElsewhere = ({ onReload }: { onReload: () => void }) => {
  const { t } = useI18n("staff");
  return (
    <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Text size="sm">{t("changedElsewhere")}</Text>
        <Button size="xs" variant="light" color="yellow" onClick={onReload}>
          {t("reloadDraft")}
        </Button>
      </Group>
    </Alert>
  );
};

/**
 * What the person wants (staff competence design S8–S10, §7): their
 * interests, their preferences and their availability declaration, under
 * *What I want* on My page and *What they want* on the person page. The
 * person edits each list and saves it whole; their manager and HR read it
 * and stamp it **Seen**, row by row, so a wish added since stands out as
 * new. No free text anywhere, on purpose: a reason is a health fact in a
 * sentence (N§2). Rendered only for a reader the server lets read it
 * (`canReadWishes`).
 */
export const PersonWishes = ({ person, self, timeZone }: { person: StaffPerson; self: boolean; timeZone?: string }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const stamps = person.capabilities.canStampWishesSeen;
  const interests = useQuery(interestsQueryOptions(person.id));
  const preferences = useQuery(preferencesQueryOptions(person.id));
  const availability = useQuery(availabilityQueryOptions(person.id));
  const competences = useQuery({ ...personCompetencesQueryOptions(person.id), enabled: stamps });
  const queries = [interests, preferences, availability];
  const failed = queries.find((query) => query.isError);

  // Seen has something to stamp while any wish, or any current competence, carries no stamp.
  const unseen = [
    ...(interests.data?.interests ?? []),
    ...(preferences.data?.preferences ?? []),
    ...(availability.data?.declarations ?? []),
    ...(competences.data ?? []).filter((row) => !row.superseded),
  ].some((row) => !row.seenAt);

  const seen = useMutation({
    mutationFn: () => markWishesSeen(person.id),
    onSuccess: async (counts) => {
      notifications.show({
        color: "teal",
        title: t("markedSeen"),
        message: t("markedSeenRows", {
          count: counts.interests + counts.preferences + counts.declarations + counts.competences,
        }),
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotMarkSeen"), message: refusalText(error, t) }),
  });

  return (
    <FieldGroup
      title={self ? t("whatIWant") : t("whatTheyWant")}
      description={t("wishesDescription")}
      actions={
        stamps && (
          <Button
            variant="light"
            leftSection={<IconCheck size={16} />}
            disabled={!unseen || queries.some((query) => !query.data)}
            loading={seen.isPending}
            onClick={() => seen.mutate()}
          >
            {t("markSeen")}
          </Button>
        )
      }
    >
      {failed?.error && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadWishes")}>
          {failed.error.message}
        </Alert>
      )}
      {queries.some((query) => query.isPending) && <ContentSkeleton rows={3} rowHeight={44} />}
      {interests.data && (
        <Interests
          personId={person.id}
          rows={interests.data.interests}
          self={self}
          stamps={stamps}
          timeZone={timeZone}
        />
      )}
      {preferences.data && (
        <Preferences
          personId={person.id}
          rows={preferences.data.preferences}
          self={self}
          stamps={stamps}
          timeZone={timeZone}
        />
      )}
      {availability.data && (
        <Availability
          personId={person.id}
          rows={availability.data.declarations}
          self={self}
          stamps={stamps}
          timeZone={timeZone}
        />
      )}
    </FieldGroup>
  );
};

interface SectionProps<T> {
  personId: number;
  rows: T[];
  self: boolean;
  stamps: boolean;
  timeZone?: string;
}

const Section = ({ title, description, children }: { title: string; description: string; children: ReactNode }) => (
  <Stack gap="xs" component="section" aria-label={title}>
    <div>
      <Title order={4} size="h5">
        {title}
      </Title>
      <Text size="sm" c="dimmed">
        {description}
      </Text>
    </div>
    {children}
  </Stack>
);

/** Interests: what the person wants to learn, or to use more of what they hold (design S8). */
const Interests = ({ personId, rows, self, stamps, timeZone }: SectionProps<StaffPersonInterest>) => {
  const { t, locale } = useI18n("staff");
  const catalogue = useQuery({ ...competencesQueryOptions(), enabled: self });
  const categories = useQuery({ ...competenceCategoriesQueryOptions(), enabled: self });
  const name = (row: StaffPersonInterest) => {
    const named = competenceName(row.competence, locale);
    return row.competence.archived ? t("competenceArchivedName", { name: named }) : named;
  };
  return (
    <Section title={t("interests")} description={t("interestsDescription")}>
      {self && (
        <InterestsEditor
          personId={personId}
          rows={rows}
          catalogue={catalogue.data ?? []}
          categories={categories.data ?? []}
          nameOf={name}
        />
      )}
      {rows.length === 0 ? (
        !self && <EmptyState title={t("noInterests")} />
      ) : (
        <List spacing={4} listStyleType="none" aria-label={t("interests")}>
          {rows.map((row) => (
            <List.Item key={row.competenceId}>
              <Group gap="xs" wrap="wrap">
                <Text size="sm" fw={500}>
                  {name(row)}
                </Text>
                <Badge variant="light" color={row.held ? "grape" : "cyan"}>
                  {row.held ? t("wantsMore") : t("wantsToLearn")}
                </Badge>
                <SeenMark seenAt={row.seenAt} stamps={stamps} timeZone={timeZone} />
              </Group>
            </List.Item>
          ))}
        </List>
      )}
    </Section>
  );
};

const InterestsEditor = ({
  personId,
  rows,
  catalogue,
  categories,
  nameOf,
}: {
  personId: number;
  rows: StaffPersonInterest[];
  catalogue: StaffCompetence[];
  categories: StaffCompetenceCategory[];
  nameOf: (row: StaffPersonInterest) => string;
}) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const {
    draft: chosen,
    setDraft: setChosen,
    dirty,
    stale,
    reload,
  } = useServerDraft(
    rows.map((row) => String(row.competenceId)),
    interestsKey,
  );
  const [banner, setBanner] = useState<string[]>([]);

  // The offered catalogue, and what is chosen already even if no longer offered, by category.
  const options = new Map<string, { label: string; categoryId: number }>();
  for (const one of offeredCompetences(catalogue))
    options.set(String(one.id), { label: competenceName(one, locale), categoryId: one.categoryId });
  for (const row of rows)
    options.set(String(row.competenceId), { label: nameOf(row), categoryId: row.competence.categoryId });
  const order = new Map(categories.map((category, index) => [category.id, index]));
  const groups = [...new Set([...options.values()].map((one) => one.categoryId))].sort(
    (a, b) => (order.get(a) ?? Number.MAX_SAFE_INTEGER) - (order.get(b) ?? Number.MAX_SAFE_INTEGER),
  );
  const data = groups.map((categoryId) => {
    const category = categories.find((one) => one.id === categoryId);
    return {
      group: category ? competenceName(category, locale) : t("competenceCategoryUnknown"),
      items: [...options.entries()]
        .filter(([, one]) => one.categoryId === categoryId)
        .map(([value, one]) => ({ value, label: one.label })),
    };
  });

  const save = useMutation({
    mutationFn: (ids: string[]) =>
      replaceInterests(personId, { interests: ids.map((id) => ({ competenceId: Number(id) })) }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("interestsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error, ids) => setBanner(listRefusal(error, t, (index) => options.get(ids[index])?.label)),
  });

  return (
    <Stack gap="xs">
      {stale && (
        <ChangedElsewhere
          onReload={() => {
            reload();
            setBanner([]);
          }}
        />
      )}
      <Banner lines={banner} />
      <MultiSelect
        label={t("interestsPick")}
        placeholder={t("competencePickPlaceholder")}
        nothingFoundMessage={t("competenceNothingFound")}
        searchable
        clearable
        maxValues={20}
        data={data}
        value={chosen}
        onChange={setChosen}
      />
      <Group justify="flex-end">
        <Button
          loading={save.isPending}
          disabled={!dirty}
          onClick={() => {
            setBanner([]);
            save.mutate(chosen);
          }}
        >
          {t("saveInterests")}
        </Button>
      </Group>
    </Stack>
  );
};

/** A preference kind's choice as the editor holds it, every kind's fields in one shape. */
interface PreferenceDraft {
  on: boolean;
  weight: string;
  days: string[];
  parts: string[];
  locationIds: string[];
  choice: string | null;
  count: number | string;
}

const draftsOf = (rows: StaffPersonPreference[]): Record<StaffPreferenceKind, PreferenceDraft> =>
  Object.fromEntries(
    preferenceKinds.map((kind) => {
      const row = rows.find((one) => one.kind === kind);
      const value = (row?.value ?? {}) as Record<string, unknown>;
      const list = (key: string) => ((value[key] as unknown[] | undefined) ?? []).map(String);
      return [
        kind,
        {
          on: row !== undefined,
          weight: String(row?.weight ?? 2),
          days: kind === "weekdays" ? list("days") : [],
          parts: list("parts"),
          locationIds: list("locationIds"),
          choice: (value.extent ?? value.share ?? null) as string | null,
          count: ((kind === "max_consecutive_days" ? value.days : value.hours) as number | undefined) ?? "",
        },
      ];
    }),
  ) as Record<StaffPreferenceKind, PreferenceDraft>;

const whole = (value: number | string, min: number, max: number) =>
  value !== "" && Number.isInteger(Number(value)) && Number(value) >= min && Number(value) <= max;

/** The value a kind's draft says, in the canonical order the server keeps it; nothing while incomplete. */
const preferenceValue = (kind: StaffPreferenceKind, draft: PreferenceDraft): Record<string, unknown> | undefined => {
  const numbers = (values: string[]) => values.map(Number).sort((a, b) => a - b);
  switch (kind) {
    case "weekdays":
      return draft.days.length > 0 ? { days: numbers(draft.days) } : undefined;
    case "time_of_day":
      return draft.parts.length > 0
        ? { parts: timeOfDayParts.filter((part) => draft.parts.includes(part)) }
        : undefined;
    case "location":
      return draft.locationIds.length > 0 ? { locationIds: numbers(draft.locationIds) } : undefined;
    case "travel":
      return draft.choice ? { extent: draft.choice } : undefined;
    case "remote":
      return draft.choice ? { share: draft.choice } : undefined;
    case "max_consecutive_days":
      return whole(draft.count, 1, 14) ? { days: Number(draft.count) } : undefined;
    case "max_hours_per_week":
      return whole(draft.count, 1, 60) ? { hours: Number(draft.count) } : undefined;
  }
};

/**
 * The drafts as one string, for telling two equal: each kind off, or on
 * with its weight and its value in the canonical order — incomplete, a
 * draft no saved list can equal.
 */
const preferencesKey = (drafts: Record<StaffPreferenceKind, PreferenceDraft>): string =>
  JSON.stringify(
    preferenceKinds.map((kind) =>
      drafts[kind].on ? [drafts[kind].weight, preferenceValue(kind, drafts[kind]) ?? "incomplete"] : null,
    ),
  );

/** The places a preference names, by name: `/meta`'s unarchived locations, every caller's. */
const useLocations = () => {
  const meta = useQuery(staffMetaQueryOptions());
  return meta.data?.locations ?? [];
};

/** A preference's value in words: the days, the parts, the places, the extent, the share, the limit. */
const usePreferenceWords = () => {
  const { t, formatters } = useI18n("staff");
  const locations = useLocations();
  return (row: StaffPersonPreference): string => {
    const value = row.value as Record<string, unknown>;
    const list = (key: string) => (value[key] as unknown[] | undefined) ?? [];
    switch (row.kind) {
      case "weekdays":
        return list("days")
          .map((day) => weekdayLabel(formatters.formatDate, Number(day)))
          .join(", ");
      case "time_of_day":
        return list("parts")
          .map((part) => timeOfDayLabel(t, String(part)))
          .join(", ");
      case "location":
        return list("locationIds")
          .map((id) => locations.find((one) => one.id === Number(id))?.name ?? t("preferenceLocationUnknown"))
          .join(", ");
      case "travel":
        return travelLabel(t, String(value.extent));
      case "remote":
        return remoteLabel(t, String(value.share));
      case "max_consecutive_days":
        return t("consecutiveDaysValue", { count: value.days });
      case "max_hours_per_week":
        return t("hoursPerWeekValue", { count: value.hours });
    }
  };
};

/** Preferences (design S9): one per kind, each with its weight — a card per kind for the person. */
const Preferences = ({ personId, rows, self, stamps, timeZone }: SectionProps<StaffPersonPreference>) => {
  const { t } = useI18n("staff");
  const words = usePreferenceWords();
  const ordered = preferenceKinds.flatMap((kind) => rows.filter((row) => row.kind === kind));
  return (
    <Section title={t("preferences")} description={t("preferencesDescription")}>
      {self ? (
        <PreferencesEditor personId={personId} rows={ordered} timeZone={timeZone} />
      ) : ordered.length === 0 ? (
        <EmptyState title={t("noPreferences")} />
      ) : (
        <List spacing={4} listStyleType="none" aria-label={t("preferences")}>
          {ordered.map((row) => (
            <List.Item key={row.kind}>
              <Group gap="xs" wrap="wrap">
                <Text size="sm">
                  <Text span fw={500}>
                    {preferenceKindLabel(t, row.kind)}
                  </Text>
                  {`: ${words(row)}`}
                </Text>
                <Badge variant="outline">{weightLabel(t, row.weight)}</Badge>
                <SeenMark seenAt={row.seenAt} stamps={stamps} timeZone={timeZone} />
              </Group>
            </List.Item>
          ))}
        </List>
      )}
    </Section>
  );
};

const PreferencesEditor = ({
  personId,
  rows,
  timeZone,
}: {
  personId: number;
  rows: StaffPersonPreference[];
  timeZone?: string;
}) => {
  const { t, formatters } = useI18n("staff");
  const queryClient = useQueryClient();
  const phone = useMediaQuery("(max-width: 48em)");
  const locations = useLocations();
  const { draft: drafts, setDraft: setDrafts, stale, reload } = useServerDraft(draftsOf(rows), preferencesKey);
  const [errors, setErrors] = useState<Partial<Record<StaffPreferenceKind, string>>>({});
  const [banner, setBanner] = useState<string[]>([]);
  const change = (kind: StaffPreferenceKind, patch: Partial<PreferenceDraft>) => {
    setDrafts((current) => ({ ...current, [kind]: { ...current[kind], ...patch } }));
    setErrors((current) => ({ ...current, [kind]: undefined }));
  };

  const save = useMutation({
    mutationFn: (sent: StaffPersonPreference[]) => replacePreferences(personId, { preferences: sent }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("preferencesSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error, sent) =>
      setBanner(listRefusal(error, t, (index) => (sent[index] ? preferenceKindLabel(t, sent[index].kind) : undefined))),
  });

  const submit = () => {
    setBanner([]);
    const incomplete: Partial<Record<StaffPreferenceKind, string>> = {};
    const sent: StaffPersonPreference[] = [];
    for (const kind of preferenceKinds) {
      const draft = drafts[kind];
      if (!draft.on) continue;
      const value = preferenceValue(kind, draft);
      if (value) sent.push({ kind, value, weight: Number(draft.weight) });
      else incomplete[kind] = t("preferenceIncomplete");
    }
    setErrors(incomplete);
    if (Object.keys(incomplete).length === 0) save.mutate(sent);
  };

  // A chosen location archived since is no longer listed: it stays among the choices, unnamed.
  const locationData = (chosen: string[]) => [
    ...locations.map((one) => ({ value: String(one.id), label: one.name })),
    ...chosen
      .filter((id) => !locations.some((one) => String(one.id) === id))
      .map((id) => ({ value: id, label: t("preferenceLocationUnknown") })),
  ];

  const control = (kind: StaffPreferenceKind, draft: PreferenceDraft) => {
    switch (kind) {
      case "weekdays":
        return (
          <Checkbox.Group label={t("preferenceDays")} value={draft.days} onChange={(days) => change(kind, { days })}>
            <Group gap="sm" mt={4}>
              {ISO_WEEKDAYS.map((day) => (
                <Checkbox key={day} value={String(day)} label={weekdayLabel(formatters.formatDate, day)} />
              ))}
            </Group>
          </Checkbox.Group>
        );
      case "time_of_day":
        return (
          <Checkbox.Group
            label={t("preferenceParts")}
            value={draft.parts}
            onChange={(parts) => change(kind, { parts })}
          >
            <Group gap="sm" mt={4}>
              {timeOfDayParts.map((part) => (
                <Checkbox key={part} value={part} label={timeOfDayLabel(t, part)} />
              ))}
            </Group>
          </Checkbox.Group>
        );
      case "location":
        return (
          <MultiSelect
            label={t("preferenceLocations")}
            searchable
            maxValues={10}
            data={locationData(draft.locationIds)}
            value={draft.locationIds}
            onChange={(locationIds) => change(kind, { locationIds })}
          />
        );
      case "travel":
      case "remote":
        return (
          <Select
            label={kind === "travel" ? t("preferenceTravel") : t("preferenceRemote")}
            allowDeselect={false}
            data={
              kind === "travel"
                ? travelExtents.map((value) => ({ value, label: travelLabel(t, value) }))
                : remoteShares.map((value) => ({ value, label: remoteLabel(t, value) }))
            }
            value={draft.choice}
            onChange={(choice) => change(kind, { choice })}
          />
        );
      case "max_consecutive_days":
      case "max_hours_per_week":
        return (
          <NumberInput
            label={kind === "max_consecutive_days" ? t("preferenceConsecutiveDays") : t("preferenceHoursPerWeek")}
            min={1}
            max={kind === "max_consecutive_days" ? 14 : 60}
            allowDecimal={false}
            allowNegative={false}
            value={draft.count}
            onChange={(count) => change(kind, { count })}
          />
        );
    }
  };

  return (
    <Stack gap="sm">
      {stale && (
        <ChangedElsewhere
          onReload={() => {
            reload();
            setErrors({});
            setBanner([]);
          }}
        />
      )}
      <Banner lines={banner} />
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="sm">
        {preferenceKinds.map((kind) => {
          const draft = drafts[kind];
          const row = rows.find((one) => one.kind === kind);
          return (
            <Card key={kind} withBorder radius="md" padding="sm" role="group" aria-label={preferenceKindLabel(t, kind)}>
              <Stack gap="xs">
                <Group justify="space-between" wrap="wrap" gap="xs">
                  <Text fw={500}>{preferenceKindLabel(t, kind)}</Text>
                  {row && <SeenMark seenAt={row.seenAt} stamps={false} timeZone={timeZone} />}
                </Group>
                <Switch
                  label={t("havePreference")}
                  checked={draft.on}
                  onChange={(event) => change(kind, { on: event.currentTarget.checked })}
                />
                {draft.on && (
                  <>
                    {control(kind, draft)}
                    <SegmentedControl
                      aria-label={t("weight")}
                      fullWidth={phone}
                      data={WEIGHTS.map((weight) => ({ value: String(weight), label: weightLabel(t, weight) }))}
                      value={draft.weight}
                      onChange={(weight) => change(kind, { weight })}
                    />
                  </>
                )}
                {errors[kind] && (
                  <Text size="sm" c="red">
                    {errors[kind]}
                  </Text>
                )}
              </Stack>
            </Card>
          );
        })}
      </SimpleGrid>
      <Group justify="flex-end">
        <Button loading={save.isPending} onClick={submit}>
          {t("savePreferences")}
        </Button>
      </Group>
    </Stack>
  );
};

/** A declaration's day or days in words: every Friday (from, until), or the dates. */
const useDeclarationWords = () => {
  const { t, date } = useStaffFormat();
  const { formatters } = useI18n("staff");
  return {
    when: (row: StaffAvailabilityDeclaration) => {
      if (row.weekday === undefined) {
        const from = row.fromDate ? date(row.fromDate) : "";
        return row.toDate && row.toDate !== row.fromDate ? `${from} – ${date(row.toDate)}` : from;
      }
      return [
        t("everyWeekday", { weekday: weekdayLabel(formatters.formatDate, row.weekday) }),
        ...(row.fromDate ? [t("availabilityFrom", { date: date(row.fromDate) })] : []),
        ...(row.toDate ? [t("availabilityUntil", { date: date(row.toDate) })] : []),
      ].join(" ");
    },
    hours: (row: StaffAvailabilityDeclaration) =>
      row.startMinute !== undefined && row.endMinute !== undefined
        ? `${minutesLabel(row.startMinute)}–${minutesLabel(row.endMinute)}`
        : t("allDay"),
  };
};

/** The availability declaration (design S10): recurring and dated rows, data only. */
const Availability = ({ personId, rows, self, stamps, timeZone }: SectionProps<StaffAvailabilityDeclaration>) => {
  const { t } = useI18n("staff");
  return (
    <Section title={t("availability")} description={t("availabilityDescription")}>
      {self ? (
        <AvailabilityEditor personId={personId} rows={rows} timeZone={timeZone} />
      ) : rows.length === 0 ? (
        <EmptyState title={t("noDeclarations")} />
      ) : (
        <DeclarationsTable rows={rows} stamps={stamps} timeZone={timeZone} />
      )}
    </Section>
  );
};

const DeclarationsTable = ({
  rows,
  stamps,
  timeZone,
  onRemove,
}: {
  rows: StaffAvailabilityDeclaration[];
  stamps: boolean;
  timeZone?: string;
  onRemove?: (index: number) => void;
}) => {
  const { t } = useI18n("staff");
  const words = useDeclarationWords();
  return (
    <Table.ScrollContainer minWidth={480}>
      <Table aria-label={t("availability")} verticalSpacing="xs">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("availabilityKind")}</Table.Th>
            <Table.Th>{t("availabilityWhen")}</Table.Th>
            <Table.Th>{t("availabilityHours")}</Table.Th>
            <Table.Th>
              <VisuallyHidden>{t("rowActions")}</VisuallyHidden>
            </Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row, index) => (
            <Table.Tr key={JSON.stringify(declarationInput(row))}>
              <Table.Td>
                <Badge
                  variant="light"
                  color={row.kind === "unavailable" ? "red" : row.kind === "preferred" ? "grape" : "teal"}
                >
                  {availabilityKindLabel(t, row.kind)}
                </Badge>
              </Table.Td>
              <Table.Td>{words.when(row)}</Table.Td>
              <Table.Td>{words.hours(row)}</Table.Td>
              <Table.Td>
                <Group gap="xs" justify="flex-end" wrap="nowrap">
                  <SeenMark seenAt={row.seenAt} stamps={stamps} timeZone={timeZone} />
                  {onRemove && (
                    <ActionIcon
                      variant="subtle"
                      color="red"
                      aria-label={t("removeDeclaration", { what: words.when(row) })}
                      onClick={() => onRemove(index)}
                    >
                      <IconTrash size={16} />
                    </ActionIcon>
                  )}
                </Group>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

const AvailabilityEditor = ({
  personId,
  rows,
  timeZone,
}: {
  personId: number;
  rows: StaffAvailabilityDeclaration[];
  timeZone?: string;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const phone = useMediaQuery("(max-width: 48em)");
  const words = useDeclarationWords();
  const { draft, setDraft, dirty, stale, reload } = useServerDraft(rows, declarationsKey);
  const [adding, setAdding] = useState(false);
  const [banner, setBanner] = useState<string[]>([]);

  const save = useMutation({
    mutationFn: (sent: StaffAvailabilityDeclaration[]) =>
      replaceAvailability(personId, { declarations: sent.map(declarationInput) }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("availabilitySaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error, sent) =>
      setBanner(listRefusal(error, t, (index) => (sent[index] ? words.when(sent[index]) : undefined))),
  });

  return (
    <Stack gap="xs">
      {stale && (
        <ChangedElsewhere
          onReload={() => {
            reload();
            setBanner([]);
          }}
        />
      )}
      <Banner lines={banner} />
      {draft.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("noDeclarations")}
        </Text>
      ) : (
        <DeclarationsTable
          rows={draft}
          stamps={false}
          timeZone={timeZone}
          onRemove={(index) => setDraft((current) => current.filter((_, at) => at !== index))}
        />
      )}
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Button variant="light" leftSection={<IconPlus size={16} />} onClick={() => setAdding(true)}>
          {t("addDeclaration")}
        </Button>
        <Button
          loading={save.isPending}
          disabled={!dirty}
          onClick={() => {
            setBanner([]);
            save.mutate(draft);
          }}
        >
          {t("saveAvailability")}
        </Button>
      </Group>
      <Modal opened={adding} onClose={() => setAdding(false)} title={t("addDeclaration")} fullScreen={phone}>
        {adding && (
          <DeclarationForm
            existing={draft}
            onAdd={(row) => {
              setDraft((current) => [...current, row]);
              setAdding(false);
            }}
            onCancel={() => setAdding(false)}
          />
        )}
      </Modal>
    </Stack>
  );
};

interface DeclarationValues {
  kind: string;
  repeats: "recurring" | "dated";
  weekday: string | null;
  fromDate: string | null;
  toDate: string | null;
  partOfDay: boolean;
  start: string | null;
  end: string | null;
}

/** Add declaration: available, unavailable or preferred, every week or on dates, all day or part of it. */
const DeclarationForm = ({
  existing,
  onAdd,
  onCancel,
}: {
  existing: StaffAvailabilityDeclaration[];
  onAdd: (row: StaffAvailabilityDeclaration) => void;
  onCancel: () => void;
}) => {
  const { t, formatters } = useI18n("staff");
  const [duplicate, setDuplicate] = useState(false);
  const form = useForm<DeclarationValues>({
    initialValues: {
      kind: "unavailable",
      repeats: "recurring",
      weekday: null,
      fromDate: null,
      toDate: null,
      partOfDay: false,
      start: "480",
      end: "960",
    },
    // The duplicate is the values' it was found for: changed, they are judged anew on Add.
    onValuesChange: () => setDuplicate(false),
    validate: {
      weekday: (value, values) => (values.repeats === "recurring" && !value ? t("declarationWeekdayRequired") : null),
      fromDate: (value, values) => (values.repeats === "dated" && !value ? t("declarationFromRequired") : null),
      toDate: (value, values) =>
        value && values.fromDate && value < values.fromDate ? t("declarationToBeforeFrom") : null,
      end: (value, values) =>
        values.partOfDay && Number(value) <= Number(values.start) ? t("declarationEndBeforeStart") : null,
    },
  });
  const rowOf = (values: DeclarationValues): StaffAvailabilityDeclaration => ({
    kind: values.kind as StaffAvailabilityDeclaration["kind"],
    ...(values.repeats === "recurring" && values.weekday ? { weekday: Number(values.weekday) } : {}),
    ...(values.fromDate ? { fromDate: values.fromDate } : {}),
    ...(values.toDate ? { toDate: values.toDate } : {}),
    ...(values.partOfDay ? { startMinute: Number(values.start), endMinute: Number(values.end) } : {}),
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        const row = rowOf(values);
        if (existing.some((one) => sameDeclaration(one, row))) {
          setDuplicate(true);
          return;
        }
        onAdd(row);
      })}
    >
      <Stack>
        {duplicate && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {t("declarationDuplicate")}
          </Alert>
        )}
        <Select
          label={t("availabilityKind")}
          allowDeselect={false}
          data={availabilityKinds.map((value) => ({ value, label: availabilityKindLabel(t, value) }))}
          {...form.getInputProps("kind")}
        />
        <Radio.Group label={t("declarationRepeats")} {...form.getInputProps("repeats")}>
          <Group gap="md" mt="xs">
            <Radio value="recurring" label={t("declarationRecurring")} />
            <Radio value="dated" label={t("declarationDated")} />
          </Group>
        </Radio.Group>
        {form.values.repeats === "recurring" && (
          <Select
            label={t("declarationWeekday")}
            withAsterisk
            allowDeselect={false}
            data={ISO_WEEKDAYS.map((day) => ({
              value: String(day),
              label: weekdayLabel(formatters.formatDate, day),
            }))}
            {...form.getInputProps("weekday")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("declarationFrom")}
            description={form.values.repeats === "recurring" ? t("declarationBoundsDescription") : undefined}
            withAsterisk={form.values.repeats === "dated"}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("fromDate")}
          />
          <DateInput
            label={t("declarationTo")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("toDate")}
          />
        </SimpleGrid>
        <Switch label={t("declarationPartOfDay")} {...form.getInputProps("partOfDay", { type: "checkbox" })} />
        {form.values.partOfDay && (
          <SimpleGrid cols={2}>
            <Select
              label={t("declarationStart")}
              allowDeselect={false}
              data={startOptions()}
              {...form.getInputProps("start")}
            />
            <Select
              label={t("declarationEnd")}
              allowDeselect={false}
              data={endOptions()}
              {...form.getInputProps("end")}
            />
          </SimpleGrid>
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onCancel}>
            {t("cancel")}
          </Button>
          <Button type="submit">{t("add")}</Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * Reviewed together (design S11): when the manager and the person last went
 * through the Competence tab together, **Reviewed today** for whoever may
 * verify, and the earlier reviews behind a switch. No notes, no goals.
 */
export const PersonReviews = ({ person }: { person: StaffPerson }) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const [earlier, setEarlier] = useState(false);
  const reviews = useQuery({ ...competenceReviewsQueryOptions(person.id), enabled: earlier });
  const last = person.lastCompetenceReview;

  const record = useMutation({
    mutationFn: () => recordCompetenceReview(person.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("reviewRecorded"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRecordReview"), message: refusalText(error, t) }),
  });

  const confirm = () =>
    modals.openConfirmModal({
      title: t("reviewedTodayTitle"),
      children: <Text size="sm">{t("reviewedTodayExplained", { name: person.displayName })}</Text>,
      labels: { confirm: t("reviewedToday"), cancel: t("cancel") },
      onConfirm: () => record.mutate(),
    });

  return (
    <FieldGroup
      title={t("reviewedTogether")}
      description={t("reviewedTogetherDescription")}
      actions={
        person.capabilities.canVerifyCompetence && (
          <Button variant="light" leftSection={<IconCheck size={16} />} loading={record.isPending} onClick={confirm}>
            {t("reviewedToday")}
          </Button>
        )
      }
    >
      <Fields
        items={[
          [
            t("lastReviewed"),
            last ? t("lastReviewedBy", { date: date(last.reviewedOn), name: last.reviewer }) : t("neverReviewed"),
          ],
        ]}
      />
      {last && (
        <Switch
          label={t("showEarlierReviews")}
          checked={earlier}
          onChange={(event) => setEarlier(event.currentTarget.checked)}
        />
      )}
      {earlier && reviews.isPending && <ContentSkeleton rows={2} rowHeight={28} />}
      {earlier && reviews.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadReviews")}>
          {reviews.error.message}
        </Alert>
      )}
      {earlier && reviews.data?.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noReviews")}
        </Text>
      )}
      {earlier && reviews.data && reviews.data.length > 0 && (
        <List size="sm" aria-label={t("earlierReviews")}>
          {reviews.data.map((review) => (
            <List.Item key={review.id}>
              {t("reviewLine", { date: date(review.reviewedOn), name: review.reviewer })}
            </List.Item>
          ))}
        </List>
      )}
    </FieldGroup>
  );
};

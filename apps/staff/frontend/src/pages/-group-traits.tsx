import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  MultiSelect,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type SupportedLocale, useI18n, useLocale } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  type StaffGroup,
  type StaffGroupProvider,
  type StaffGroupTraitDeclaration,
  setGroupTraits,
} from "../api/groups";
import { STAFF_QUERY_KEY } from "../api/request";
import { staffSettingsQueryOptions } from "../api/settings";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { mappedRefusals } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { awaitsConfirmation, groupErrorWords, syncStateColor, traitText } from "../lib/groups";

/** A trait's value as the form holds it: a bool, a string (empty for none), a choice (null for none), a list. */
type TraitValue = boolean | string | string[] | null;
type TraitValues = Record<string, TraitValue>;

/** The form's start: the values Staff stores, a trait without one at its declared default, else empty. */
const initialValuesOf = (traits: StaffGroupTraitDeclaration[], stored: Record<string, unknown>): TraitValues =>
  Object.fromEntries(
    traits.map((trait) => {
      const value = trait.key in stored ? stored[trait.key] : trait.default;
      switch (trait.type) {
        case "bool":
          return [trait.key, value === true];
        case "string":
          return [trait.key, typeof value === "string" ? value : ""];
        case "choice":
          return [trait.key, typeof value === "string" && value !== "" ? value : null];
        case "list":
          return [trait.key, Array.isArray(value) ? value.filter((one): one is string => typeof one === "string") : []];
        default:
          return [trait.key, null];
      }
    }),
  );

/** What the request sends: a value of the trait's type, an empty string or choice left out. */
const valuesOut = (traits: StaffGroupTraitDeclaration[], values: TraitValues): Record<string, unknown> =>
  Object.fromEntries(
    traits.flatMap((trait): [string, unknown][] => {
      const value = values[trait.key];
      if (trait.type === "bool") return [[trait.key, value === true]];
      if (trait.type === "list") return [[trait.key, Array.isArray(value) ? value : []]];
      if (typeof value === "string" && value.trim() !== "") return [[trait.key, value.trim()]];
      return [];
    }),
  );

const isEmpty = (value: TraitValue) =>
  value === null || (typeof value === "string" && value.trim() === "") || (Array.isArray(value) && value.length === 0);

/**
 * One card per provider in the slot (Entra design D10, D14), whether HR has
 * it on for the group or not; the form is the provider's declaration and
 * nothing else — no trait is known here by name.
 */
export const GroupProviders = ({ group, providers }: { group: StaffGroup; providers: StaffGroupProvider[] }) => (
  <>
    {providers.map((provider) => (
      // A new answer from the server is a fresh card: the switch and the form start from it.
      <ProviderCard
        key={`${provider.key}:${provider.enabled}:${JSON.stringify(provider.values)}:${provider.state}`}
        group={group}
        provider={provider}
      />
    ))}
  </>
);

/**
 * The section's state as a badge: *Waiting*, *Found in … and awaiting
 * confirmation there*, *In sync since …*, *Failed*, *Kept, renamed*,
 * *Deleted*.
 */
const StateBadge = ({ provider }: { provider: StaffGroupProvider }) => {
  const { t } = useI18n("staff");
  const { formatters } = useI18n("staff");
  if (!provider.state) return null;
  const unconfirmed = awaitsConfirmation(provider);
  const label = unconfirmed
    ? t("groupAwaitingConfirmation", { provider: provider.name })
    : provider.state === "synced" && provider.syncedAt
      ? t("groupSyncedSince", {
          date: formatters.formatDate(new Date(provider.syncedAt), { dateStyle: "medium", timeStyle: "short" }),
        })
      : t(`groupSyncState_${provider.state}`);
  return (
    <Badge variant="light" color={unconfirmed ? "orange" : syncStateColor[provider.state]}>
      {label}
    </Badge>
  );
};

const ProviderCard = ({ group, provider }: { group: StaffGroup; provider: StaffGroupProvider }) => {
  const { t } = useStaffFormat();
  const locale = useLocale();
  const queryClient = useQueryClient();
  const settings = useQuery(staffSettingsQueryOptions());
  const archived = Boolean(group.archivedAt);
  const traits = provider.traits;
  const [on, setOn] = useState(provider.enabled);
  const form = useForm<TraitValues>({
    initialValues: initialValuesOf(traits, provider.values),
    validate: Object.fromEntries(
      traits.map((trait) => [
        trait.key,
        (value: TraitValue) => (trait.required && isEmpty(value) ? t("traitRequired") : null),
      ]),
    ),
  });

  const save = useMutation({
    mutationFn: (enabled: boolean) =>
      setGroupTraits(
        group.id,
        provider.key,
        enabled ? { enabled, values: valuesOut(traits, form.values) } : { enabled },
      ),
    onSuccess: async (_saved, enabled) => {
      notifications.show({
        color: "teal",
        title: t(enabled ? "groupProviderSaved" : "groupProviderTurnedOff", { provider: provider.name }),
        message: "",
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "groups"] });
    },
    onError: (error, enabled) => {
      if (!enabled) setOn(true);
      // The declaration's rules come back keyed by the trait; put each beside its field.
      const keys = traits.map((trait) => trait.key);
      const { fields, rest } = mappedRefusals(error, (field) => {
        const key = field.startsWith("values.") ? field.slice("values.".length) : field;
        return keys.includes(key) ? key : undefined;
      });
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });

  const days = settings.data?.deleteExternalAfterDays;
  const turnOff = () => {
    // Never on for the group: nothing to retire, nothing sent.
    if (!provider.enabled) {
      setOn(false);
      return;
    }
    modals.openConfirmModal({
      title: t("groupProviderOffTitle", { provider: provider.name, name: group.name }),
      children: (
        <Text size="sm">
          {settings.data === undefined
            ? t("groupProviderOffRetire", { provider: provider.name })
            : typeof days === "number"
              ? t("groupProviderOffDelete", { provider: provider.name, count: days })
              : t("groupProviderOffKeep", { provider: provider.name, name: group.name })}
        </Text>
      ),
      labels: { confirm: t("groupProviderOffConfirm"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => {
        setOn(false);
        save.mutate(false);
      },
    });
  };

  // The provider's own refusal, kept on the section until the values change, beside its field.
  const failedField =
    provider.lastError && provider.lastErrorField && traits.some((trait) => trait.key === provider.lastErrorField)
      ? provider.lastErrorField
      : undefined;
  const failure = provider.lastError ? groupErrorWords(t, provider.lastError) : undefined;
  const unreadable = provider.status === "failing" && traits.length === 0;

  return (
    <FieldGroup title={provider.name} actions={<StateBadge provider={provider} />}>
      {unreadable && (
        <Alert color="red" icon={<IconAlertCircle size={16} />}>
          {t("groupProviderUnreadable", { provider: provider.name })}
        </Alert>
      )}
      {provider.status === "unconfigured" && (
        <Text size="sm" c="dimmed">
          {t("groupProviderUnconfigured", { provider: provider.name })}
        </Text>
      )}
      {provider.state === "synced" && provider.enabled && (
        <Group gap="xs" wrap="nowrap">
          <IconCircleCheck size={16} color="var(--mantine-color-teal-6)" />
          <Text size="sm">{t("groupStaffsInProviderToo", { provider: provider.name })}</Text>
        </Group>
      )}
      {failure && !failedField && (
        <Alert color={provider.state === "failed" ? "red" : "yellow"} icon={<IconAlertCircle size={16} />}>
          {provider.state === "failed" ? t("groupFailedBecause", { reason: failure }) : failure}
        </Alert>
      )}
      {!unreadable && (
        <Switch
          label={t("groupInProvider", { provider: provider.name })}
          checked={on}
          disabled={archived || save.isPending}
          onChange={(event) => (event.currentTarget.checked ? setOn(true) : turnOff())}
        />
      )}
      {on && !unreadable && (
        <form onSubmit={form.onSubmit(() => save.mutate(true))} aria-label={provider.name}>
          <Stack gap="sm">
            {traits.map((trait) => (
              <TraitField
                key={trait.key}
                trait={trait}
                locale={locale}
                disabled={archived}
                inputProps={form.getInputProps(trait.key, trait.type === "bool" ? { type: "checkbox" } : undefined)}
                failed={failedField === trait.key && failure ? t("groupFailedBecause", { reason: failure }) : undefined}
              />
            ))}
            {!archived && (
              <Group justify="flex-end">
                <Button type="submit" loading={save.isPending}>
                  {t("save")}
                </Button>
              </Group>
            )}
          </Stack>
        </form>
      )}
    </FieldGroup>
  );
};

/**
 * One trait as its declaration says: `bool` a switch, `string` a text with
 * its length, `choice` a select, `list` a multi-select; the label and the
 * description in the reader's language, English when the provider has none;
 * the provider's page as *About this setting*; a required one marked.
 */
const TraitField = ({
  trait,
  locale,
  disabled,
  inputProps,
  failed,
}: {
  trait: StaffGroupTraitDeclaration;
  locale: SupportedLocale;
  disabled: boolean;
  inputProps: ReturnType<ReturnType<typeof useForm<TraitValues>>["getInputProps"]>;
  /** The provider's refusal of this trait at the last sync, said when the form has none of its own. */
  failed?: string;
}) => {
  const { t } = useI18n("staff");
  const label = traitText(trait.label, locale) || trait.key;
  const words = traitText(trait.description, locale);
  const description: ReactNode =
    words || trait.docUrl ? (
      <>
        {words}
        {words && trait.docUrl ? " " : null}
        {trait.docUrl && (
          <Anchor href={trait.docUrl} target="_blank" rel="noopener noreferrer" size="xs">
            {t("aboutThisSetting")}
          </Anchor>
        )}
      </>
    ) : undefined;
  const error = (inputProps.error as ReactNode) ?? failed;
  const common = { label, description, disabled, ...inputProps, error };
  const choices = (trait.choices ?? []).map((choice) => ({
    value: choice.value,
    label: traitText(choice.label, locale) || choice.value,
  }));

  switch (trait.type) {
    case "bool":
      return <Switch {...common} />;
    case "choice":
      return <Select {...common} withAsterisk={trait.required} data={choices} clearable={!trait.required} />;
    case "list":
      return <MultiSelect {...common} withAsterisk={trait.required} data={choices} clearable searchable />;
    default:
      return <TextInput {...common} withAsterisk={trait.required} maxLength={trait.maxLength} />;
  }
};

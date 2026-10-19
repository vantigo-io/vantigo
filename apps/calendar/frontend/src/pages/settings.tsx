import "../styles/fullcalendar.css";
import {
  Alert,
  Badge,
  Button,
  Card,
  CheckIcon,
  ColorSwatch,
  Group,
  Input,
  Modal,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useId, useState } from "react";
import {
  deleteFeedAddress,
  type FeedAddress,
  type FeedVariant,
  feedAddressesQueryOptions,
  resetFeedAddress,
} from "../api/feed";
import { ApiConflictError, ApiValidationError, CALENDAR_QUERY_KEY } from "../api/request";
import {
  type CalendarSubscription,
  type CalendarSubscriptionColour,
  type CalendarSubscriptionWrite,
  createSubscription,
  deleteSubscription,
  refreshSubscription,
  SUBSCRIPTION_LIMIT,
  subscriptionsQueryOptions,
  updateSubscription,
} from "../api/subscriptions";
import "../i18n";
import { colourForName, SUBSCRIPTION_COLOURS } from "../lib/colours";

/** "2 hours ago", "in 3 days", "now": the largest whole unit, in the viewer's language. */
const relativeTime = (instant: string, locale: string): string => {
  const seconds = (Date.parse(instant) - Date.now()) / 1000;
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 365 * 86400],
    ["month", 30 * 86400],
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
  ];
  const format = new Intl.RelativeTimeFormat(locale === "nb" ? "nb-NO" : "en-US", { numeric: "auto" });
  for (const [unit, size] of units)
    if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
  return format.format(0, "second");
};

/** The address shortened to its host, for the table; one that does not parse is shown whole. */
const hostOf = (url: string): string => {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
};

/**
 * My calendar (feed design D9): the person's own calendar addresses — the
 * full calendar and a busy-only one — and the calendars from elsewhere they
 * subscribe to. Everything on it is the caller's own; the host guards the
 * route with `calendar:access`.
 */
export const SettingsPage = () => {
  const { t } = useI18n("calendar");
  return (
    <Stack gap="lg">
      <PageHeader title={t("settings.title")} description={t("settings.description")} />
      <FeedSection />
      <SubscriptionsSection />
    </Stack>
  );
};

const FeedSection = () => {
  const { t } = useI18n("calendar");
  const feed = useQuery(feedAddressesQueryOptions());
  const addressOf = (variant: FeedVariant) => feed.data?.addresses.find((one) => one.variant === variant);

  return (
    <Stack gap="sm">
      <Stack gap={0}>
        <Title order={4}>{t("settings.phoneHeading")}</Title>
        <Text size="sm" c="dimmed">
          {t("settings.phoneDescription")}
        </Text>
      </Stack>
      {feed.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("settings.couldNotLoadAddresses")}>
          {feed.error.message}
        </Alert>
      )}
      {feed.isPending && <ContentSkeleton rows={2} rowHeight={120} />}
      {feed.data && (
        <>
          <SimpleGrid cols={{ base: 1, md: 2 }}>
            <FeedCard variant="full" address={addressOf("full")} />
            <FeedCard variant="busy" address={addressOf("busy")} />
          </SimpleGrid>
          <Text size="sm" c="dimmed">
            {t("settings.refreshNote")}
          </Text>
          {!feed.data.public && (
            <Text size="sm" c="dimmed">
              {t("settings.networkOnly")}
            </Text>
          )}
        </>
      )}
    </Stack>
  );
};

const FeedCard = ({ variant, address }: { variant: FeedVariant; address?: FeedAddress }) => {
  const { t, locale, formatters } = useI18n("calendar");
  const queryClient = useQueryClient();
  const titleId = useId();
  const title = t(variant === "full" ? "settings.full" : "settings.busy");

  const reset = useMutation({
    mutationFn: () => resetFeedAddress(variant),
    onSuccess: async () => {
      notifications.show({
        color: "teal",
        title: t(address ? "settings.addressReset" : "settings.addressCreated"),
        message: "",
      });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("settings.couldNotChangeAddress"), message: error.message }),
  });

  const turnOff = useMutation({
    mutationFn: () => deleteFeedAddress(variant),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("settings.addressTurnedOff"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("settings.couldNotTurnOff"), message: error.message }),
  });

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      notifications.show({ color: "teal", title: t("settings.copied"), message: "" });
    } catch {
      notifications.show({ color: "red", title: t("settings.couldNotCopy"), message: "" });
    }
  };

  const confirmReset = () =>
    modals.openConfirmModal({
      title: t("settings.resetTitle"),
      children: <Text size="sm">{t("settings.resetConfirm")}</Text>,
      labels: { confirm: t("settings.reset"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => reset.mutate(),
    });

  const confirmTurnOff = () =>
    modals.openConfirmModal({
      title: t("settings.turnOffTitle"),
      children: <Text size="sm">{t("settings.turnOffConfirm")}</Text>,
      labels: { confirm: t("settings.turnOff"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => turnOff.mutate(),
    });

  const created = address
    ? formatters.formatDate(address.createdAt, { day: "numeric", month: "short", year: "numeric" })
    : "";

  return (
    <Card withBorder padding="lg" radius="md" role="region" aria-labelledby={titleId}>
      <Stack gap="sm">
        <Stack gap={0}>
          <Title order={5} id={titleId}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {t(variant === "full" ? "settings.fullDescription" : "settings.busyDescription")}
          </Text>
        </Stack>
        {address ? (
          <>
            <TextInput label={t("settings.address")} value={address.address} readOnly />
            <Group gap="xs">
              <Button size="xs" variant="light" onClick={() => void copy(address.address)}>
                {t("settings.copy")}
              </Button>
              <Button size="xs" variant="light" onClick={() => void copy(address.webcalAddress)}>
                {t("settings.copyWebcal")}
              </Button>
            </Group>
            <Text size="xs" c="dimmed">
              {address.lastUsedAt
                ? t("settings.createdLastRead", { date: created, relative: relativeTime(address.lastUsedAt, locale) })
                : t("settings.createdNeverRead", { date: created })}
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" loading={reset.isPending} onClick={confirmReset}>
                {t("settings.reset")}
              </Button>
              <Button size="xs" variant="subtle" color="red" loading={turnOff.isPending} onClick={confirmTurnOff}>
                {t("settings.turnOff")}
              </Button>
            </Group>
          </>
        ) : (
          <Group>
            <Button size="sm" loading={reset.isPending} onClick={() => reset.mutate()}>
              {t("settings.createAddress")}
            </Button>
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/** The form's state: a new subscription, or one being edited. */
type FormState = { mode: "create" } | { mode: "edit"; subscription: CalendarSubscription };

const SubscriptionsSection = () => {
  const { t, locale } = useI18n("calendar");
  const queryClient = useQueryClient();
  const subscriptions = useQuery(subscriptionsQueryOptions());
  const [form, setForm] = useState<FormState | null>(null);

  const refresh = useMutation({
    mutationFn: (id: number) => refreshSubscription(id),
    onSuccess: async (row) => {
      if (!row.error) notifications.show({ color: "teal", title: t("settings.refreshed"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("settings.couldNotRefresh"), message: error.message }),
  });

  const remove = useMutation({
    mutationFn: (id: number) => deleteSubscription(id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("settings.removed"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
    },
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("settings.couldNotRemove"), message: error.message }),
  });

  const confirmRemove = (subscription: CalendarSubscription) =>
    modals.openConfirmModal({
      title: t("settings.removeTitle"),
      children: <Text size="sm">{t("settings.removeConfirm", { name: subscription.name })}</Text>,
      labels: { confirm: t("settings.remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(subscription.id),
    });

  const rows = subscriptions.data ?? [];

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="start">
          <Stack gap={0}>
            <Title order={4}>{t("settings.elsewhereHeading")}</Title>
            <Text size="sm" c="dimmed">
              {t("settings.elsewhereDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setForm({ mode: "create" })}>
            {t("settings.addCalendar")}
          </Button>
        </Group>

        {subscriptions.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("settings.couldNotLoadSubscriptions")}>
            {subscriptions.error.message}
          </Alert>
        )}
        {subscriptions.isPending && <ContentSkeleton rows={2} rowHeight={44} />}
        {subscriptions.data && rows.length === 0 && (
          <EmptyState title={t("settings.noSubscriptions")} description={t("settings.noSubscriptionsDescription")} />
        )}

        {rows.length > 0 && (
          <Table aria-label={t("settings.elsewhereHeading")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("settings.columnCalendar")}</Table.Th>
                <Table.Th>{t("settings.columnShown")}</Table.Th>
                <Table.Th>{t("settings.columnUpdated")}</Table.Th>
                <Table.Th>{t("settings.columnActions")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((subscription) => (
                <Table.Tr key={subscription.id}>
                  <Table.Td>
                    <Group gap="sm" wrap="nowrap">
                      <ColorSwatch color={colourForName(subscription.colour)} size={14} withShadow={false} />
                      <Stack gap={0} style={{ minWidth: 0 }}>
                        <Text size="sm">{subscription.name}</Text>
                        <Text size="xs" c="dimmed" truncate>
                          {hostOf(subscription.url)}
                        </Text>
                      </Stack>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    {subscription.busyOnly && (
                      <Badge variant="light" color="gray">
                        {t("settings.busyOnlyBadge")}
                      </Badge>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">
                      {subscription.fetchedAt
                        ? t("settings.updated", { relative: relativeTime(subscription.fetchedAt, locale) })
                        : t("settings.notFetched")}
                    </Text>
                    {subscription.error && (
                      <Text size="xs" c="red">
                        {subscription.error}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        loading={refresh.isPending && refresh.variables === subscription.id}
                        onClick={() => refresh.mutate(subscription.id)}
                      >
                        {t("settings.refreshNow")}
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        onClick={() => setForm({ mode: "edit", subscription })}
                      >
                        {t("settings.edit")}
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="red"
                        onClick={() => confirmRemove(subscription)}
                      >
                        {t("settings.remove")}
                      </Button>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}

        <Modal
          opened={form !== null}
          onClose={() => setForm(null)}
          title={t(form?.mode === "edit" ? "settings.editTitle" : "settings.addTitle")}
        >
          {form && (
            <SubscriptionForm
              key={form.mode === "edit" ? form.subscription.id : "new"}
              subscription={form.mode === "edit" ? form.subscription : undefined}
              taken={rows.map((row) => row.colour)}
              onDone={() => setForm(null)}
            />
          )}
        </Modal>
      </Stack>
    </Card>
  );
};

interface SubscriptionFormProps {
  subscription?: CalendarSubscription;
  /** The colours the other subscriptions have, so a new one starts on a free one. */
  taken: readonly CalendarSubscriptionColour[];
  onDone: () => void;
}

const SubscriptionForm = ({ subscription, taken, onDone }: SubscriptionFormProps) => {
  const { t } = useI18n("calendar");
  const queryClient = useQueryClient();
  const colourLabelId = useId();
  const form = useForm<CalendarSubscriptionWrite>({
    initialValues: subscription
      ? {
          name: subscription.name,
          url: subscription.url,
          colour: subscription.colour,
          busyOnly: subscription.busyOnly,
        }
      : {
          name: "",
          url: "",
          colour: SUBSCRIPTION_COLOURS.find((colour) => !taken.includes(colour)) ?? SUBSCRIPTION_COLOURS[0],
          busyOnly: false,
        },
    validate: {
      name: (value) => (value.trim() ? null : t("settings.nameRequired")),
      url: (value) => (value.trim() ? null : t("settings.urlRequired")),
    },
  });

  const save = useMutation({
    mutationFn: (values: CalendarSubscriptionWrite) => {
      const write = { ...values, name: values.name.trim(), url: values.url.trim() };
      return subscription ? updateSubscription(subscription.id, write) : createSubscription(write);
    },
    onSuccess: async (row) => {
      // The save succeeded whatever the fetch answered: the row is kept, and its error says why.
      if (row.error) notifications.show({ color: "yellow", title: t("settings.savedButFailed"), message: row.error });
      else
        notifications.show({
          color: "teal",
          title: t(subscription ? "settings.saved" : "settings.added"),
          message: "",
        });
      await queryClient.invalidateQueries({ queryKey: [CALENDAR_QUERY_KEY] });
      onDone();
    },
    onError: (error: Error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        form.setErrors({ name: fields.name, url: fields.url, colour: fields.colour, busyOnly: fields.busyOnly });
        return;
      }
      const message =
        error instanceof ApiConflictError && error.code === SUBSCRIPTION_LIMIT
          ? t("settings.limitReached")
          : error.message;
      notifications.show({ color: "red", title: t("settings.couldNotSave"), message });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <TextInput label={t("settings.name")} withAsterisk maxLength={100} {...form.getInputProps("name")} />
        <TextInput
          label={t("settings.url")}
          description={t("settings.urlHint")}
          withAsterisk
          maxLength={2048}
          {...form.getInputProps("url")}
        />
        <Input.Wrapper label={t("settings.colour")} labelElement="div" id={colourLabelId} error={form.errors.colour}>
          <Group gap={6} mt={4} role="radiogroup" aria-labelledby={`${colourLabelId}-label`}>
            {SUBSCRIPTION_COLOURS.map((colour) => {
              const checked = form.values.colour === colour;
              return (
                <ColorSwatch
                  key={colour}
                  component="button"
                  type="button"
                  role="radio"
                  aria-checked={checked}
                  aria-label={t(`settings.colour_${colour}`)}
                  color={colourForName(colour)}
                  size={26}
                  style={{ cursor: "pointer", color: "var(--mantine-color-white)" }}
                  onClick={() => form.setFieldValue("colour", colour)}
                >
                  {checked && <CheckIcon size={12} />}
                </ColorSwatch>
              );
            })}
          </Group>
        </Input.Wrapper>
        <Switch
          label={t("settings.busyOnly")}
          description={t("settings.busyOnlyHint")}
          {...form.getInputProps("busyOnly", { type: "checkbox" })}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

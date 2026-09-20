import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Input,
  NumberInput,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconAlertTriangle,
  IconArrowDown,
  IconArrowUp,
  IconPencil,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type ExpenseCategory, expenseCategoriesQueryOptions, updateExpenseCategory } from "../api/categories";
import { deleteExpenseRate, type ExpenseRate, expenseRatesQueryOptions, resetExpenseRateKind } from "../api/rates";
import { ApiValidationError, EXPENSES_QUERY_KEY } from "../api/request";
import { type ExpenseSettings, expenseSettingsQueryOptions, updateExpenseSettings } from "../api/settings";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useDecimalSeparator, useExpenseFormat } from "../lib/format";
import { groupRatesByKind, isPercentageRateKind, rateKindHintKey, rateKindLabelKey } from "../lib/rate-kinds";
import { CategoryFormModal, type CategoryModalState } from "./-category-form-modal";
import { RateFormModal, type RateModalState } from "./-rate-form-modal";

const numeric = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * Expense settings (design §3.5, §3.3, §3.4): what a new expense starts with,
 * the receipt rule, the period lock, the dated rate table and the categories.
 *
 * The page is `expenses:manage`'s — the host guards the route — and needs no
 * props: everything it may do it reads from the endpoints themselves.
 */
export const SettingsPage = () => {
  const { t } = useI18n("expenses");
  const settings = useQuery(expenseSettingsQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader title={t("settings")} description={t("settingsDescription")} />
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {settings.error.message}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={4} rowHeight={52} />}
      {settings.data && <GeneralForm settings={settings.data} />}
      <RatesSection defaultCurrency={settings.data?.defaultCurrency ?? ""} />
      <CategoriesSection />
    </Stack>
  );
};

/** Off / Always / Over an amount — the three things `receiptRequiredOver` can say. */
type ReceiptRule = "off" | "always" | "over";

interface GeneralFormValues {
  defaultCurrency: string;
  defaultMarkupPercent: number | string;
  receiptRule: ReceiptRule;
  receiptRequiredOver: number | string;
  lockedBefore: string | null;
  timeZone: string;
}

/**
 * The settings as the form holds them. The receipt rule is three states, not
 * two: an absent threshold is "off", zero is "always", and a number is "over".
 */
const formValuesOf = (settings: ExpenseSettings): GeneralFormValues => ({
  defaultCurrency: settings.defaultCurrency,
  defaultMarkupPercent: settings.defaultMarkupPercent ?? 0,
  receiptRule:
    settings.receiptRequiredOver === undefined ? "off" : settings.receiptRequiredOver === 0 ? "always" : "over",
  receiptRequiredOver: settings.receiptRequiredOver ?? "",
  lockedBefore: settings.lockedBefore ?? null,
  timeZone: settings.timeZone,
});

/**
 * Every IANA name this browser knows, so the zone is picked rather than
 * typed. A runtime that has never heard of `supportedValuesOf` — and a
 * jsdom that answers nothing — still has to offer the zone the installation
 * already runs on, or a save would silently clear it.
 */
const timeZoneOptions = (current: string): string[] => {
  const known = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return known.includes(current) ? [...known] : [current, ...known];
};

const GeneralForm = ({ settings }: { settings: ExpenseSettings }) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const decimalSeparator = useDecimalSeparator();
  const queryClient = useQueryClient();

  const form = useForm<GeneralFormValues>({
    initialValues: formValuesOf(settings),
    validate: {
      defaultCurrency: (value) => (/^[A-Za-z]{3}$/.test(value.trim()) ? null : t("currencyRequired")),
      defaultMarkupPercent: (value) => {
        const markup = numeric(value);
        return markup === undefined || markup < 0 || markup > 1000 ? t("markupRange") : null;
      },
      receiptRequiredOver: (value, values) => {
        if (values.receiptRule !== "over") return null;
        const amount = numeric(value);
        return amount === undefined || amount <= 0 ? t("receiptThresholdRequired") : null;
      },
      timeZone: (value) => (value.trim() ? null : t("timeZoneRequired")),
    },
  });

  /**
   * Mantine captures `initialValues` on the first mount, so an open form
   * would keep showing a lock date somebody else has since changed — and the
   * confirm below would then compare against a date that is no longer on
   * screen. A pristine form follows the server; one being typed into is left
   * alone, because nobody's work is thrown away for a background refetch.
   * State adjusted during render from the previous render's value, the way
   * React documents.
   */
  const loaded = JSON.stringify(settings);
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    // Asked once: `setInitialValues` moves the baseline `isDirty` compares
    // against, so asking again afterwards would answer "dirty" for a form
    // nobody has touched.
    if (!form.isDirty()) {
      const next = formValuesOf(settings);
      form.setInitialValues(next);
      form.setValues(next);
    }
  }

  const save = useMutation({
    mutationFn: (values: GeneralFormValues) =>
      updateExpenseSettings({
        defaultCurrency: values.defaultCurrency.trim().toUpperCase(),
        defaultMarkupPercent: numeric(values.defaultMarkupPercent) ?? 0,
        timeZone: values.timeZone,
        ...(values.receiptRule === "always"
          ? { receiptRequiredOver: 0 }
          : values.receiptRule === "over"
            ? { receiptRequiredOver: numeric(values.receiptRequiredOver) ?? 0 }
            : {}),
        ...(values.lockedBefore ? { lockedBefore: values.lockedBefore } : {}),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("settingsSaved"), message: "" });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const fields = error.fieldErrors;
        form.setErrors({
          defaultCurrency: fields.defaultCurrency,
          defaultMarkupPercent: fields.defaultMarkupPercent,
          receiptRequiredOver: fields.receiptRequiredOver,
          lockedBefore: fields.lockedBefore,
          // A name neither Go nor Postgres knows is refused on this very
          // field, so it lands on the select the choice was made in.
          timeZone: fields.timeZone,
        });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: refusalMessage(error) });
    },
  });

  /** The lock closes a period for everybody but a manager, so it is confirmed out loud. */
  /**
   * Two changes are asked about out loud, and the zone is the heavier of the
   * two: the lock closes a period and can be reopened, while the zone **moves
   * the days of every trip already recorded** and cannot put them back. The
   * standing warning beside the field is not the same thing as being asked.
   */
  const submit = (values: GeneralFormValues) => {
    const lockChanged = values.lockedBefore !== (settings.lockedBefore ?? null);
    const zoneChanged = values.timeZone !== settings.timeZone;
    if (!lockChanged && !zoneChanged) {
      save.mutate(values);
      return;
    }
    modals.openConfirmModal({
      title: zoneChanged ? t("timeZoneChangeTitle") : t("lockChangeTitle"),
      children: (
        <Stack gap="xs">
          {zoneChanged && (
            <Text size="sm">{t("timeZoneChangeConfirm", { from: settings.timeZone, to: values.timeZone })}</Text>
          )}
          {lockChanged && (
            <Text size="sm">
              {values.lockedBefore
                ? t("lockSetConfirm", { date: format.date(values.lockedBefore) })
                : t("lockClearConfirm")}
            </Text>
          )}
        </Stack>
      ),
      labels: { confirm: t("save"), cancel: t("cancel") },
      onConfirm: () => save.mutate(values),
    });
  };

  return (
    <Card withBorder padding="lg" radius="md">
      <form onSubmit={form.onSubmit(submit)}>
        <Stack>
          <Title order={5}>{t("generalSettings")}</Title>
          <Group grow align="start">
            <TextInput label={t("defaultCurrency")} withAsterisk {...form.getInputProps("defaultCurrency")} />
            <NumberInput
              label={t("defaultMarkupPercent")}
              description={t("defaultMarkupDescription")}
              min={0}
              max={1000}
              decimalScale={2}
              decimalSeparator={decimalSeparator}
              {...form.getInputProps("defaultMarkupPercent")}
            />
          </Group>

          <Input.Wrapper label={t("receiptRule")} labelElement="div" description={t("receiptRuleDescription")}>
            <SegmentedControl
              mt={4}
              aria-label={t("receiptRule")}
              value={form.values.receiptRule}
              onChange={(value) => form.setFieldValue("receiptRule", value as ReceiptRule)}
              data={[
                { value: "off", label: t("receiptRuleOff") },
                { value: "always", label: t("receiptRuleAlways") },
                { value: "over", label: t("receiptRuleOver") },
              ]}
            />
          </Input.Wrapper>
          {form.values.receiptRule === "over" && (
            <NumberInput
              label={t("receiptThreshold")}
              description={form.values.defaultCurrency}
              min={0}
              decimalScale={2}
              decimalSeparator={decimalSeparator}
              {...form.getInputProps("receiptRequiredOver")}
            />
          )}

          <DateInput
            label={t("lockDate")}
            description={t("lockDateDescription")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("lockedBefore")}
          />

          <Stack gap={4}>
            <Select
              label={t("businessTimeZone")}
              description={t("businessTimeZoneDescription")}
              withAsterisk
              searchable
              allowDeselect={false}
              data={timeZoneOptions(settings.timeZone)}
              {...form.getInputProps("timeZone")}
            />
            {/* Not a colour and not an icon alone: the sentence says what
                changing it does, because a trip's days move with it. */}
            <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title={t("timeZoneMovesDays")}>
              {t("timeZoneMovesDaysDescription")}
            </Alert>
          </Stack>

          <Group justify="flex-end">
            <Button type="submit" loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
};

const RatesSection = ({ defaultCurrency }: { defaultCurrency: string }) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const queryClient = useQueryClient();
  const { data, isPending, isError, error } = useQuery(expenseRatesQueryOptions());
  const [modalState, setModalState] = useState<RateModalState | null>(null);

  const remove = useMutation({
    mutationFn: (id: number) => deleteExpenseRate(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("rateDeleted"), message: "" });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotDeleteRate"), message: refusalMessage(failure) }),
  });

  const reset = useMutation({
    mutationFn: (kind: string) => resetExpenseRateKind(kind),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("rateKindReset"), message: "" });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotResetRateKind"), message: refusalMessage(failure) }),
  });

  const confirmDelete = (rate: ExpenseRate) =>
    modals.openConfirmModal({
      title: t("deleteRateTitle"),
      children: <Text size="sm">{t("deleteRateConfirm", { date: format.date(rate.validFrom) })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(rate.id),
    });

  const confirmReset = (kind: string) =>
    modals.openConfirmModal({
      title: t("resetRateKindTitle"),
      children: <Text size="sm">{t("resetRateKindConfirm", { kind: t(rateKindLabelKey(kind)) })}</Text>,
      labels: { confirm: t("resetRateKind"), cancel: t("cancel") },
      onConfirm: () => reset.mutate(kind),
    });

  const groups = groupRatesByKind(data);

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Stack gap={0}>
            <Title order={5}>{t("rates")}</Title>
            <Text size="sm" c="dimmed">
              {t("ratesDescription")}
            </Text>
          </Stack>
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => setModalState({ mode: "create", kind: "mileage" })}
          >
            {t("addRate")}
          </Button>
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadRates")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={4} rowHeight={44} />}

        {data &&
          groups.map((group) => (
            <Stack key={group.kind} gap={4} data-rate-kind={group.kind}>
              <Group justify="space-between" wrap="wrap">
                <Stack gap={0}>
                  <Title order={6}>{t(rateKindLabelKey(group.kind))}</Title>
                  {rateKindHintKey(group.kind) && (
                    <Text size="xs" c="dimmed" maw={640}>
                      {t(rateKindHintKey(group.kind) as string)}
                    </Text>
                  )}
                </Stack>
                <Group gap="xs">
                  <Button
                    size="xs"
                    variant="default"
                    onClick={() => setModalState({ mode: "create", kind: group.kind })}
                  >
                    {t("addRateTo", { kind: t(rateKindLabelKey(group.kind)) })}
                  </Button>
                  <Button size="xs" variant="subtle" onClick={() => confirmReset(group.kind)}>
                    {t("resetRateKindOf", { kind: t(rateKindLabelKey(group.kind)) })}
                  </Button>
                </Group>
              </Group>

              {group.rates.length === 0 ? (
                <Text size="sm" c="dimmed">
                  {t("noRatesForKind")}
                </Text>
              ) : (
                <Table aria-label={t(rateKindLabelKey(group.kind))}>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("validFrom")}</Table.Th>
                      <Table.Th>{t("rateValue")}</Table.Th>
                      <Table.Th>{t("rateSource")}</Table.Th>
                      <Table.Th>{t("rowActions")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {group.rates.map((rate) => {
                      const named = `${t(rateKindLabelKey(rate.kind))} ${format.date(rate.validFrom)}`;
                      return (
                        <Table.Tr key={rate.id} data-rate={rate.id}>
                          <Table.Td>{format.date(rate.validFrom)}</Table.Td>
                          <Table.Td>
                            {isPercentageRateKind(rate.kind)
                              ? format.percent(rate.value)
                              : format.money(rate.value, rate.currency ?? defaultCurrency)}
                          </Table.Td>
                          <Table.Td>
                            <Badge variant="light" color={rate.source ? "blue" : "gray"}>
                              {rate.source ? t("stateRate") : t("ownRate")}
                            </Badge>
                          </Table.Td>
                          <Table.Td>
                            <Group gap={4} wrap="nowrap">
                              <ActionIcon
                                variant="subtle"
                                aria-label={t("editRate", { rate: named })}
                                onClick={() => setModalState({ mode: "edit", rate })}
                              >
                                <IconPencil size={16} />
                              </ActionIcon>
                              <ActionIcon
                                variant="subtle"
                                color="red"
                                aria-label={t("deleteRate", { rate: named })}
                                onClick={() => confirmDelete(rate)}
                              >
                                <IconTrash size={16} />
                              </ActionIcon>
                            </Group>
                          </Table.Td>
                        </Table.Tr>
                      );
                    })}
                  </Table.Tbody>
                </Table>
              )}
            </Stack>
          ))}

        <RateFormModal state={modalState} defaultCurrency={defaultCurrency} onClose={() => setModalState(null)} />
      </Stack>
    </Card>
  );
};

const CategoriesSection = () => {
  const { t } = useI18n("expenses");
  const queryClient = useQueryClient();
  const { data, isPending, isError, error } = useQuery(expenseCategoriesQueryOptions());
  const [modalState, setModalState] = useState<CategoryModalState | null>(null);

  const change = useMutation({
    mutationFn: ({ category, changes }: { category: ExpenseCategory; changes: Partial<ExpenseCategory> }) =>
      updateExpenseCategory(category.id, {
        name: changes.name ?? category.name,
        active: changes.active ?? category.active,
        position: changes.position ?? category.position,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [EXPENSES_QUERY_KEY] });
    },
    onError: (failure) =>
      notifications.show({ color: "red", title: t("couldNotSaveCategory"), message: refusalMessage(failure) }),
  });

  const categories = data ?? [];

  /**
   * Moving a category is a replace of its position; the server closes the gap
   * and renumbers the rest, so the one being passed does not have to be moved
   * too.
   */
  const move = (category: ExpenseCategory, by: -1 | 1) =>
    change.mutate({ category, changes: { position: Math.max(1, category.position + by) } });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Stack gap={0}>
            <Title order={5}>{t("categories")}</Title>
            <Text size="sm" c="dimmed">
              {t("categoriesDescription")}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={() => setModalState({ mode: "create" })}>
            {t("addCategory")}
          </Button>
        </Group>

        {isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCategories")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={44} />}

        {data && categories.length === 0 && (
          <EmptyState title={t("noCategories")} description={t("noCategoriesDescription")} />
        )}

        {categories.length > 0 && (
          <Table aria-label={t("categories")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("categoryName")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th>{t("rowActions")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {categories.map((category, index) => (
                <Table.Tr key={category.id} data-category={category.id}>
                  <Table.Td>{category.name}</Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={category.active ? "green" : "gray"}>
                      {category.active ? t("categoryIsActive") : t("categoryIsInactive")}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4} wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        disabled={index === 0}
                        aria-label={t("moveCategoryUp", { name: category.name })}
                        onClick={() => move(category, -1)}
                      >
                        <IconArrowUp size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        disabled={index === categories.length - 1}
                        aria-label={t("moveCategoryDown", { name: category.name })}
                        onClick={() => move(category, 1)}
                      >
                        <IconArrowDown size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        aria-label={t("editCategory", { name: category.name })}
                        onClick={() => setModalState({ mode: "edit", category })}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        onClick={() => change.mutate({ category, changes: { active: !category.active } })}
                      >
                        {category.active
                          ? t("deactivateCategory", { name: category.name })
                          : t("reactivateCategory", { name: category.name })}
                      </Button>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}

        <CategoryFormModal state={modalState} onClose={() => setModalState(null)} />
      </Stack>
    </Card>
  );
};

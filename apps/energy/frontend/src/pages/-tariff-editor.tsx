import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Drawer,
  Group,
  Menu,
  NumberInput,
  SegmentedControl,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { type UseFormReturnType, useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArrowDown, IconArrowUp, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useEffect } from "react";
import { ApiValidationError } from "../api/request";
import {
  createTariff,
  invalidateTariffs,
  TARIFF_COMPONENT_KINDS,
  type TariffInput,
  type TariffKind,
  tariffQueryOptions,
  updateTariff,
} from "../api/tariffs";
import {
  type ComponentDraft,
  componentKindKey,
  draftFromTariff,
  ENERGY_KINDS,
  emptyDraft,
  formPath,
  newComponent,
  newTier,
  newVersion,
  pascal,
  placeablePaths,
  RATE_FIELDS,
  rateLabelKey,
  SINGLE_KINDS,
  type TariffDraft,
  type TierDraft,
  tariffInput,
  type VersionDraft,
} from "../lib/tariff-draft";
import "../i18n";

export type TariffEditorState = { mode: "create" } | { mode: "edit"; tariffId: number };

/**
 * The tariff editor, a drawer: the header (the kind chosen on create and
 * fixed after), the versions with their half-open dates, and each version's
 * components with the fields their kind uses (energy tariffs plan, reading 3).
 * The server's 400 is shown on the fields it names; a key the editor cannot
 * place is listed at the top.
 */
export const TariffEditor = ({ state, onClose }: { state: TariffEditorState | null; onClose: () => void }) => {
  const { t } = useI18n("energy");
  const client = useQueryClient();
  const editId = state?.mode === "edit" ? state.tariffId : null;
  const tariff = useQuery({ ...tariffQueryOptions(editId ?? 0), enabled: editId !== null });
  const form = useForm<TariffDraft>({
    initialValues: emptyDraft("power", t(componentKindKey("spot_energy"))),
    validate: (values) => {
      const errors: Record<string, string> = {};
      if (!values.name.trim()) errors.name = t("tariffNameRequired");
      values.versions.forEach((version, i) => {
        if (!version.validFrom) errors[`versions.${i}.validFrom`] = t("tariffValidFromRequired");
        version.components.forEach((component, j) => {
          const c = `versions.${i}.components.${j}`;
          if (!component.name.trim()) errors[`${c}.name`] = t("tariffComponentNameRequired");
          for (const field of RATE_FIELDS[component.kind])
            if (component[field] === "") errors[`${c}.${field}`] = t("tariffValueRequired");
          if (component.kind === "grid_energy" && component.hasNight)
            for (const field of ["rateNight", "dayFrom", "dayTo"] as const)
              if (component[field] === "") errors[`${c}.${field}`] = t("tariffValueRequired");
          component.tiers.forEach((tier, k) => {
            if (component.kind !== "grid_capacity") return;
            if (tier.fromKw === "") errors[`${c}.tiers.${k}.fromKw`] = t("tariffValueRequired");
            if (tier.perMonth === "") errors[`${c}.tiers.${k}.perMonth`] = t("tariffValueRequired");
          });
        });
      });
      return errors;
    },
  });

  const mutation = useMutation({
    mutationFn: (input: TariffInput) => (editId !== null ? updateTariff(editId, input) : createTariff(input)),
    onSuccess: (saved) => {
      void invalidateTariffs(client);
      notifications.show({
        color: "teal",
        title: editId !== null ? t("tariffUpdated") : t("tariffCreated"),
        message: saved.name,
      });
      onClose();
    },
    onError: (error) => {
      if (!(error instanceof ApiValidationError)) return;
      const placeable = placeablePaths(form.getValues());
      form.setErrors(
        Object.fromEntries(
          Object.entries(error.fieldErrors)
            .map(([key, message]) => [formPath(key), message])
            .filter(([path]) => placeable.has(path)),
        ),
      );
    },
  });

  useEffect(() => {
    if (!state) return;
    mutation.reset();
    form.clearErrors();
    if (state.mode === "create") form.setValues(emptyDraft("power", t(componentKindKey("spot_energy"))));
    else if (tariff.data) form.setValues(draftFromTariff(tariff.data));
    form.resetDirty();
    // The form is reset only when the drawer opens on a tariff, or that tariff loads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, tariff.data]);

  const submit = form.onSubmit((values) => mutation.mutate(tariffInput(values)));

  const values = form.getValues();
  // What the alert at the top lists: a refusal that is not a 400, or the 400's keys no field shows.
  const unplaced = (() => {
    const error = mutation.error;
    if (!error) return null;
    if (!(error instanceof ApiValidationError)) return { title: error.message, messages: [] };
    const placeable = placeablePaths(values);
    const messages = Object.entries(error.fieldErrors).filter(([key]) => !placeable.has(formPath(key)));
    return messages.length > 0 ? { title: error.message, messages } : null;
  })();
  const isEdit = state?.mode === "edit";
  const loading = isEdit && !tariff.data;
  const setKind = (kind: TariffKind) => {
    form.setValues({ ...emptyDraft(kind, t(componentKindKey("spot_energy"))), name: values.name });
  };
  const addVersion = () => {
    const last = values.versions[values.versions.length - 1];
    form.insertListItem("versions", newVersion(last?.validTo ?? ""));
  };

  return (
    <Drawer
      opened={state !== null}
      onClose={onClose}
      position="right"
      size="xl"
      title={isEdit ? t("editTariffTitle") : t("newTariffTitle")}
    >
      {loading ? (
        tariff.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {tariff.error.message}
          </Alert>
        ) : (
          <ContentSkeleton />
        )
      ) : (
        <form onSubmit={submit}>
          <Stack>
            {unplaced && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={unplaced.title}>
                {unplaced.messages.length > 0 && (
                  <Stack gap={2}>
                    {unplaced.messages.map(([key, message]) => (
                      <Text key={key} size="sm">
                        {t("tariffErrorAt", { key, message })}
                      </Text>
                    ))}
                  </Stack>
                )}
              </Alert>
            )}
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                {t("tariffKind")}
              </Text>
              {isEdit ? (
                <Group gap="xs">
                  <Badge variant="light">{t(`tariffKind${pascal(values.kind)}`)}</Badge>
                  <Text size="xs" c="dimmed">
                    {t("tariffKindFixed")}
                  </Text>
                </Group>
              ) : (
                <SegmentedControl
                  aria-label={t("tariffKind")}
                  value={values.kind}
                  onChange={(value) => setKind(value as TariffKind)}
                  data={[
                    { value: "power", label: t("tariffKindPower") },
                    { value: "grid", label: t("tariffKindGrid") },
                  ]}
                />
              )}
              <Text size="xs" c="dimmed">
                {values.kind === "power" ? t("tariffKindPowerHint") : t("tariffKindGridHint")}
              </Text>
              {form.errors.kind && (
                <Text size="xs" c="red">
                  {form.errors.kind}
                </Text>
              )}
            </Stack>
            <TextInput label={t("tariffName")} withAsterisk data-autofocus {...form.getInputProps("name")} />
            <Textarea label={t("tariffDescription")} rows={2} {...form.getInputProps("description")} />
            <Group justify="space-between">
              <Title order={4}>{t("tariffVersions")}</Title>
              <Button variant="light" size="compact-sm" leftSection={<IconPlus size={14} />} onClick={addVersion}>
                {t("addTariffVersion")}
              </Button>
            </Group>
            {form.errors.versions && (
              <Text size="sm" c="red">
                {form.errors.versions}
              </Text>
            )}
            {values.versions.map((version, i) => (
              <VersionCard
                key={version.key}
                form={form}
                index={i}
                version={version}
                tariffKind={values.kind}
                canRemove={values.versions.length > 1}
              />
            ))}
            <Group justify="flex-end">
              <Button variant="default" onClick={onClose}>
                {t("cancel")}
              </Button>
              <Button type="submit" loading={mutation.isPending}>
                {isEdit ? t("saveChanges") : t("createTariff")}
              </Button>
            </Group>
          </Stack>
        </form>
      )}
    </Drawer>
  );
};

type Form = UseFormReturnType<TariffDraft>;

const VersionCard = ({
  form,
  index,
  version,
  tariffKind,
  canRemove,
}: {
  form: Form;
  index: number;
  version: VersionDraft;
  tariffKind: TariffKind;
  canRemove: boolean;
}) => {
  const { t } = useI18n("energy");
  const path = `versions.${index}`;
  const kinds = version.components.map((component) => component.kind);
  const available = TARIFF_COMPONENT_KINDS[tariffKind].filter(
    (kind) =>
      !(SINGLE_KINDS.includes(kind) && kinds.includes(kind)) &&
      !(ENERGY_KINDS.includes(kind) && kinds.some((held) => ENERGY_KINDS.includes(held))),
  );
  const versionError = form.errors[path] ?? form.errors[`${path}.components`];
  return (
    <Card withBorder padding="sm" aria-label={t("tariffVersionNumber", { number: index + 1 })} component="section">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start">
          <Text fw={600}>{t("tariffVersionNumber", { number: index + 1 })}</Text>
          {canRemove && (
            <Button
              variant="subtle"
              color="red"
              size="compact-sm"
              leftSection={<IconTrash size={14} />}
              onClick={() => form.removeListItem("versions", index)}
            >
              {t("removeTariffVersion")}
            </Button>
          )}
        </Group>
        <Group grow align="flex-start">
          <TextInput
            type="date"
            label={t("tariffValidFrom")}
            withAsterisk
            {...form.getInputProps(`${path}.validFrom`)}
          />
          <TextInput
            type="date"
            label={t("tariffValidUntil")}
            description={t("tariffValidUntilHint")}
            {...form.getInputProps(`${path}.validTo`)}
          />
        </Group>
        {versionError && (
          <Text size="sm" c="red">
            {versionError}
          </Text>
        )}
        {version.components.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("tariffNoComponents")}
          </Text>
        )}
        {version.components.map((component, j) => (
          <ComponentCard
            key={component.key}
            form={form}
            path={`${path}.components.${j}`}
            listPath={`${path}.components`}
            index={j}
            count={version.components.length}
            component={component}
          />
        ))}
        <Menu position="bottom-start">
          <Menu.Target>
            <Button
              variant="default"
              size="compact-sm"
              leftSection={<IconPlus size={14} />}
              disabled={available.length === 0}
              style={{ alignSelf: "flex-start" }}
            >
              {t("addTariffComponent")}
            </Button>
          </Menu.Target>
          <Menu.Dropdown>
            {available.map((kind) => (
              <Menu.Item
                key={kind}
                onClick={() => form.insertListItem(`${path}.components`, newComponent(kind, t(componentKindKey(kind))))}
              >
                {t(componentKindKey(kind))}
              </Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      </Stack>
    </Card>
  );
};

const ComponentCard = ({
  form,
  path,
  listPath,
  index,
  count,
  component,
}: {
  form: Form;
  path: string;
  listPath: string;
  index: number;
  count: number;
  component: ComponentDraft;
}) => {
  const { t } = useI18n("energy");
  const kindLabel = t(componentKindKey(component.kind));
  const componentError = form.errors[path] ?? form.errors[`${path}.kind`];
  const numberProps = (field: string) => {
    const props = form.getInputProps(`${path}.${field}`);
    return { ...props, min: 0, decimalScale: 6, hideControls: true };
  };
  return (
    <Card withBorder padding="xs" bg="var(--mantine-color-default-hover)" component="section" aria-label={kindLabel}>
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap">
          <Badge variant="light">{kindLabel}</Badge>
          <Group gap={4} wrap="nowrap">
            <ActionIcon
              variant="subtle"
              aria-label={t("moveComponentUp", { name: component.name || kindLabel })}
              disabled={index === 0}
              onClick={() => form.reorderListItem(listPath, { from: index, to: index - 1 })}
            >
              <IconArrowUp size={16} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              aria-label={t("moveComponentDown", { name: component.name || kindLabel })}
              disabled={index === count - 1}
              onClick={() => form.reorderListItem(listPath, { from: index, to: index + 1 })}
            >
              <IconArrowDown size={16} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              color="red"
              aria-label={t("removeComponent", { name: component.name || kindLabel })}
              onClick={() => form.removeListItem(listPath, index)}
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        </Group>
        {componentError && (
          <Text size="sm" c="red">
            {componentError}
          </Text>
        )}
        <TextInput label={t("tariffComponentName")} withAsterisk {...form.getInputProps(`${path}.name`)} />
        {RATE_FIELDS[component.kind].length > 0 && (
          <Group grow align="flex-start">
            {RATE_FIELDS[component.kind].map((field) => (
              <NumberInput
                key={field}
                label={t(rateLabelKey(component.kind, field))}
                withAsterisk
                {...numberProps(field)}
              />
            ))}
          </Group>
        )}
        {component.kind === "spot_energy" && (
          <Text size="xs" c="dimmed">
            {t("tariffSpotEnergyHint")}
          </Text>
        )}
        {component.kind === "grid_energy" && (
          <>
            <Switch
              label={t("tariffNightRate")}
              description={t("tariffNightRateHint")}
              {...form.getInputProps(`${path}.hasNight`, { type: "checkbox" })}
            />
            {component.hasNight && (
              <>
                <Group grow align="flex-start">
                  <NumberInput label={t("tariffFieldRateNight")} withAsterisk {...numberProps("rateNight")} />
                  <NumberInput
                    label={t("tariffFieldDayFrom")}
                    withAsterisk
                    {...numberProps("dayFrom")}
                    decimalScale={0}
                    max={23}
                  />
                  <NumberInput
                    label={t("tariffFieldDayTo")}
                    withAsterisk
                    {...numberProps("dayTo")}
                    decimalScale={0}
                    max={24}
                  />
                </Group>
                <Checkbox
                  label={t("tariffFieldWeekdaysOnly")}
                  {...form.getInputProps(`${path}.weekdaysOnly`, { type: "checkbox" })}
                />
              </>
            )}
          </>
        )}
        {component.kind === "grid_capacity" && <TiersTable form={form} path={path} tiers={component.tiers} />}
      </Stack>
    </Card>
  );
};

const TiersTable = ({ form, path, tiers }: { form: Form; path: string; tiers: TierDraft[] }) => {
  const { t } = useI18n("energy");
  const tiersPath = `${path}.tiers`;
  const tiersError = form.errors[tiersPath];
  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        {t("tariffTiers")}
      </Text>
      <Text size="xs" c="dimmed">
        {t("tariffTiersHint")}
      </Text>
      <Table aria-label={t("tariffTiers")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("tariffTierFromKw")}</Table.Th>
            <Table.Th>{t("tariffTierPerMonth")}</Table.Th>
            <Table.Th />
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {tiers.map((tier, k) => (
            <Table.Tr key={tier.key}>
              <Table.Td>
                <NumberInput
                  aria-label={t("tariffTierFromKwRow", { number: k + 1 })}
                  min={0}
                  decimalScale={3}
                  hideControls
                  {...form.getInputProps(`${tiersPath}.${k}.fromKw`)}
                />
              </Table.Td>
              <Table.Td>
                <NumberInput
                  aria-label={t("tariffTierPerMonthRow", { number: k + 1 })}
                  min={0}
                  decimalScale={2}
                  hideControls
                  {...form.getInputProps(`${tiersPath}.${k}.perMonth`)}
                />
              </Table.Td>
              <Table.Td>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={t("removeTariffTier", { number: k + 1 })}
                  disabled={tiers.length === 1}
                  onClick={() => form.removeListItem(tiersPath, k)}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {tiersError && (
        <Text size="sm" c="red">
          {tiersError}
        </Text>
      )}
      <Button
        variant="subtle"
        size="compact-sm"
        leftSection={<IconPlus size={14} />}
        style={{ alignSelf: "flex-start" }}
        onClick={() => form.insertListItem(tiersPath, newTier())}
      >
        {t("addTariffTier")}
      </Button>
    </Stack>
  );
};

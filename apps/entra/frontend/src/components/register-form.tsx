import { Button, Group, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type EntraConnection, updateConnection } from "../api/connection";
import { ApiConflictError, ENTRA_QUERY_KEY } from "../api/request";
import { codeOf, fieldRefusals, isStale } from "../lib/words";
import { OwnerOnly } from "./step-card";

const guidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

interface RegisterValues {
  tenantId: string;
  clientId: string;
}

const registerValuesOf = (connection: EntraConnection): RegisterValues => ({
  tenantId: connection.tenantId ?? "",
  clientId: connection.clientId ?? "",
});

/** Step 1 for the Owner: the tenant and the client, saved under If-Match, confirmed when the module keeps accounts. */
export const RegisterForm = ({ connection }: { connection: EntraConnection }) => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  const guid = (value: string) => (guidPattern.test(value.trim()) ? null : t("guidInvalid"));
  const form = useForm<RegisterValues>({
    initialValues: registerValuesOf(connection),
    validate: { tenantId: guid, clientId: guid },
  });

  const loaded = JSON.stringify(registerValuesOf(connection));
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(registerValuesOf(connection));
      form.setValues(registerValuesOf(connection));
    }
  }

  const save = useMutation({
    mutationFn: ({ values, confirmChange }: { values: RegisterValues; confirmChange: boolean }) =>
      updateConnection(
        {
          tenantId: values.tenantId.trim().toLowerCase(),
          clientId: values.clientId.trim().toLowerCase(),
          ...(confirmChange ? { confirmChange: true } : {}),
        },
        connection.version,
      ),
    onSuccess: async (answer) => {
      queryClient.setQueryData([ENTRA_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("registerSaved"), message: t("registerSavedBody") });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: async (error, { values, confirmChange }) => {
      if (error instanceof ApiConflictError && codeOf(error) === "connection_in_use" && !confirmChange) {
        modals.openConfirmModal({
          title: t("connectionInUseTitle"),
          children: <Text size="sm">{t("connectionInUseConfirm", { count: connection.accounts.count })}</Text>,
          labels: { confirm: t("connectionInUseButton"), cancel: t("cancel") },
          confirmProps: { color: "red" },
          onConfirm: () => save.mutate({ values, confirmChange: true }),
        });
        return;
      }
      if (isStale(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("versionMismatch"), message: t("versionMismatchHint") });
        await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["tenantId", "clientId"], t);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveRegister"), message: rest.join(" ") });
    },
  });

  return (
    <form
      onSubmit={form.onSubmit((values) => save.mutate({ values, confirmChange: false }))}
      aria-label={t("step1Title")}
    >
      <Stack gap="sm">
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("tenantId")}
            description={t("tenantIdDescription")}
            withAsterisk
            {...form.getInputProps("tenantId")}
          />
          <TextInput
            label={t("clientId")}
            description={t("clientIdDescription")}
            withAsterisk
            {...form.getInputProps("clientId")}
          />
        </SimpleGrid>
        {connection.accounts.count > 0 && (
          <Text size="sm" c="orange">
            {t("registerChangeWarning", { count: connection.accounts.count })}
          </Text>
        )}
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** Step 1 for anyone else: what is set, and that the Owner sets it. */
export const RegisterRead = ({ connection }: { connection: EntraConnection }) => {
  const { t } = useI18n("entra");
  return (
    <Stack gap="xs">
      <Text size="sm">{t("tenantIdValue", { value: connection.tenantId ?? t("notSet") })}</Text>
      <Text size="sm">{t("clientIdValue", { value: connection.clientId ?? t("notSet") })}</Text>
      <OwnerOnly />
    </Stack>
  );
};

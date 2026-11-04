import { Button, Group, PasswordInput, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type GoogleConnection, updateOAuthClient } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { fieldRefusals } from "../lib/words";
import { CopyLine, OwnerOnly } from "./step-card";

const clientIdSuffix = ".apps.googleusercontent.com";

interface ClientValues {
  clientId: string;
  clientSecret: string;
}

/**
 * What is set of the Internal web client: its id, and when its secret was
 * added — never the secret, which is sealed at once and never answered.
 */
const ClientRead = ({ connection }: { connection: GoogleConnection }) => {
  const { t, formatters } = useI18n("google");
  return (
    <Stack gap={4}>
      <Text size="sm">{t("oauthClientIdValue", { value: connection.oauthClientId ?? t("notSet") })}</Text>
      <Text size="sm">
        {connection.oauthClientSecretAddedAt
          ? t("oauthSecretAddedOn", {
              date: formatters.formatDate(new Date(connection.oauthClientSecretAddedAt), { dateStyle: "medium" }),
            })
          : t("oauthSecretNone")}
      </Text>
    </Stack>
  );
};

/**
 * The client's form, the Owner's: the id, and the secret as a password field
 * that is emptied once it is sent. A changed id needs its secret; a secret
 * alone rotates the current client's.
 */
const ClientForm = ({ connection }: { connection: GoogleConnection }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const form = useForm<ClientValues>({
    initialValues: { clientId: connection.oauthClientId ?? "", clientSecret: "" },
    validate: {
      clientId: (value) => (value.trim().endsWith(clientIdSuffix) ? null : t("oauthClientIdInvalid")),
      clientSecret: (value, values) =>
        value.length === 0 &&
        (values.clientId.trim() !== (connection.oauthClientId ?? "") || !connection.oauthClientSecretAddedAt)
          ? t("oauthSecretRequired")
          : null,
    },
  });

  const loaded = connection.oauthClientId ?? "";
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty("clientId")) form.setFieldValue("clientId", loaded);
  }

  const save = useMutation({
    mutationFn: (values: ClientValues) =>
      updateOAuthClient({
        clientId: values.clientId.trim(),
        ...(values.clientSecret ? { clientSecret: values.clientSecret } : {}),
      }),
    onSuccess: async (answer) => {
      // The secret leaves the page with the answer: nothing here holds it any longer.
      queryClient.setQueryData([GOOGLE_QUERY_KEY, "connection"], answer);
      form.setValues({ clientId: answer.oauthClientId ?? "", clientSecret: "" });
      form.resetDirty({ clientId: answer.oauthClientId ?? "", clientSecret: "" });
      notifications.show({ color: "teal", title: t("oauthSaved"), message: t("oauthSavedBody") });
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (error) => {
      const { fields, rest } = fieldRefusals(error, ["clientId", "clientSecret"], t);
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSaveOauth"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))} aria-label={t("step6Title")}>
      <Stack gap="sm">
        <TextInput
          label={t("oauthClientId")}
          description={t("oauthClientIdDescription")}
          autoComplete="off"
          withAsterisk
          {...form.getInputProps("clientId")}
        />
        <PasswordInput
          label={t("oauthClientSecret")}
          description={t("oauthClientSecretDescription")}
          autoComplete="new-password"
          {...form.getInputProps("clientSecret")}
        />
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * Step 6, the Internal web client the person's own Google connection and the
 * sign-in with Google use: the redirect URIs to register, each copyable, what
 * is set, and the Owner's form.
 */
export const OAuthClientStep = ({ connection, owner }: { connection: GoogleConnection; owner: boolean }) => {
  const { t } = useI18n("google");
  return (
    <Stack gap="sm">
      <Text size="sm">{t("step6Body")}</Text>
      <Text size="sm">{t("step6Internal")}</Text>
      <Text size="sm" fw={500}>
        {t("redirectUris")}
      </Text>
      {connection.redirectUris.map((uri) => (
        <CopyLine key={uri} label={t("redirectUri")} value={uri} />
      ))}
      <Text size="sm" c="dimmed">
        {t("oauthSecretOnce")}
      </Text>
      <ClientRead connection={connection} />
      {owner ? <ClientForm connection={connection} /> : <OwnerOnly />}
    </Stack>
  );
};

import { Badge, Button, Group, PasswordInput, Stack, Table, Text, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { addCredential, type EntraCredential, revokeCredential } from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { fieldRefusals, refusalText } from "../lib/words";

const labelMax = 100;

/**
 * The client secrets, revoked ones included, newest first, with their dates
 * and which one is in use; an Owner may revoke one, and the server refuses
 * the only valid one while the module keeps accounts (`credential_in_use`).
 */
export const CredentialList = ({ credentials, owner }: { credentials: EntraCredential[]; owner: boolean }) => {
  const { t, formatters } = useI18n("entra");
  const queryClient = useQueryClient();
  const date = (value: string) => formatters.formatDate(new Date(value), { dateStyle: "medium" });
  const nameOf = (credential: EntraCredential) => credential.label || t("secretUnlabelled", { id: credential.id });

  const revoke = useMutation({
    mutationFn: (credential: EntraCredential) => revokeCredential(credential.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("secretRevoked"), message: t("secretRevokedBody") });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRevoke"), message: refusalText(error, t) }),
  });

  const askRevoke = (credential: EntraCredential) =>
    modals.openConfirmModal({
      title: t("revokeTitle", { name: nameOf(credential) }),
      children: <Text size="sm">{t("revokeConfirm")}</Text>,
      labels: { confirm: t("revoke"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => revoke.mutate(credential),
    });

  if (credentials.length === 0)
    return (
      <Text size="sm" c="dimmed">
        {t("noSecrets")}
      </Text>
    );

  return (
    <Table.ScrollContainer minWidth={560}>
      <Table verticalSpacing="xs" aria-label={t("secrets")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("secretLabel")}</Table.Th>
            <Table.Th>{t("secretCreated")}</Table.Th>
            <Table.Th>{t("secretExpires")}</Table.Th>
            <Table.Th>{t("secretLastUsed")}</Table.Th>
            <Table.Th>{t("secretState")}</Table.Th>
            {owner && <Table.Th aria-label={t("actions")} />}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {credentials.map((credential) => (
            <Table.Tr key={credential.id}>
              <Table.Td>{nameOf(credential)}</Table.Td>
              <Table.Td>{date(credential.createdAt)}</Table.Td>
              <Table.Td>{date(credential.expiresAt)}</Table.Td>
              <Table.Td>{credential.lastUsedAt ? date(credential.lastUsedAt) : t("never")}</Table.Td>
              <Table.Td>
                <Stack gap={2} align="flex-start">
                  {credential.revokedAt ? (
                    <Badge color="gray" variant="light">
                      {t("secretRevokedOn", { date: date(credential.revokedAt) })}
                    </Badge>
                  ) : credential.inUse ? (
                    <Badge color="teal">{t("inUse")}</Badge>
                  ) : (
                    <Badge color="gray" variant="outline">
                      {t("notInUse")}
                    </Badge>
                  )}
                  {credential.lastFailedAt && !credential.revokedAt && (
                    <Text size="xs" c="red">
                      {t("secretRefusedOn", { date: date(credential.lastFailedAt) })}
                    </Text>
                  )}
                </Stack>
              </Table.Td>
              {owner && (
                <Table.Td>
                  {!credential.revokedAt && (
                    <Button
                      size="compact-sm"
                      variant="subtle"
                      color="red"
                      aria-label={t("revokeNamed", { name: nameOf(credential) })}
                      loading={revoke.isPending && revoke.variables?.id === credential.id}
                      onClick={() => askRevoke(credential)}
                    >
                      {t("revoke")}
                    </Button>
                  )}
                </Table.Td>
              )}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
};

interface SecretValues {
  label: string;
  secret: string;
  expiresOn: string | null;
}

/** The start of the chosen day where the browser is: the portal shows a secret's expiry as a day. */
const expiryOf = (day: string) => new Date(`${day}T00:00:00`).toISOString();

/**
 * *Add a secret*: the label, the value as a password field — it is sent once,
 * sealed at once, and never shown again, here or anywhere — and the expiry
 * the portal showed.
 */
export const AddSecretForm = () => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  const form = useForm<SecretValues>({
    initialValues: { label: "", secret: "", expiresOn: null },
    validate: {
      label: (value) => (value.trim().length > labelMax ? t("labelTooLong", { max: labelMax }) : null),
      secret: (value) => (value.length === 0 ? t("secretRequired") : null),
      expiresOn: (value) => (value ? null : t("expiresOnRequired")),
    },
  });

  const add = useMutation({
    mutationFn: (values: SecretValues) =>
      addCredential({
        kind: "client_secret",
        ...(values.label.trim() ? { label: values.label.trim() } : {}),
        secret: values.secret,
        expiresAt: expiryOf(values.expiresOn ?? ""),
      }),
    onSuccess: async () => {
      // The value leaves the page with the answer: nothing here holds it any longer.
      form.reset();
      notifications.show({ color: "teal", title: t("secretAdded"), message: t("secretAddedBody") });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: (error) => {
      const { fields, rest } = fieldRefusals(error, ["label", "secret", "expiresAt"], t);
      const { expiresAt, ...others } = fields;
      form.setErrors({ ...others, ...(expiresAt ? { expiresOn: expiresAt } : {}) });
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotAddSecret"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => add.mutate(values))} aria-label={t("addSecret")}>
      <Stack gap="sm">
        <Text fw={500} size="sm">
          {t("addSecret")}
        </Text>
        <Group grow align="flex-start">
          <TextInput
            label={t("secretLabel")}
            description={t("secretLabelDescription")}
            {...form.getInputProps("label")}
          />
          <DateInput
            label={t("secretExpires")}
            description={t("secretExpiresDescription")}
            valueFormat={t("dateInputFormat")}
            withAsterisk
            clearable
            {...form.getInputProps("expiresOn")}
          />
        </Group>
        <PasswordInput
          label={t("secretValue")}
          description={t("secretValueDescription")}
          autoComplete="new-password"
          withAsterisk
          {...form.getInputProps("secret")}
        />
        <Group justify="flex-end">
          <Button type="submit" loading={add.isPending}>
            {t("addSecretButton")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

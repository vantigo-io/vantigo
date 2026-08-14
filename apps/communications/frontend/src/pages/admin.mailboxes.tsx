import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Loader,
  Modal,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type CreateMailboxRequest,
  createMailbox,
  type Mailbox,
  type MailboxProvider,
  mailboxesQueryOptions,
  updateMailbox,
  verifyMailbox,
} from "../api/mailboxes";
import type { ApiError } from "../api/request";
import "../i18n";

type MailboxFormValues = {
  fromAddress: string;
  displayName: string;
  provider: MailboxProvider;
  host: string;
  port: number | string;
  useSsl: boolean;
  username: string;
  password: string;
  domain: string;
  region: "us" | "eu";
  apiKey: string;
};

const initialValues: MailboxFormValues = {
  fromAddress: "",
  displayName: "",
  provider: "smtp",
  host: "",
  port: 587,
  useSsl: true,
  username: "",
  password: "",
  domain: "",
  region: "us",
  apiKey: "",
};

const credentialFields = (values: MailboxFormValues) =>
  values.provider === "smtp"
    ? {
        provider: "smtp" as const,
        smtp: {
          host: values.host,
          port: Number(values.port),
          useSsl: values.useSsl,
          username: values.username || undefined,
          password: values.password || undefined,
        },
      }
    : {
        provider: "mailgun" as const,
        mailgun: {
          domain: values.domain,
          region: values.region,
          apiKey: values.apiKey,
        },
      };

function CredentialFields({
  form,
  passwordRequired = false,
}: {
  form: ReturnType<typeof useForm<MailboxFormValues>>;
  passwordRequired?: boolean;
}) {
  const { t } = useI18n("communications");
  if (form.values.provider === "smtp") {
    return (
      <>
        <TextInput label={t("smtpHost")} required {...form.getInputProps("host")} />
        <NumberInput label={t("smtpPort")} min={1} max={65535} required {...form.getInputProps("port")} />
        <Checkbox label={t("useSsl")} {...form.getInputProps("useSsl", { type: "checkbox" })} />
        <TextInput label={t("username")} {...form.getInputProps("username")} />
        <PasswordInput
          label={t("password")}
          required={passwordRequired}
          autoComplete="new-password"
          {...form.getInputProps("password")}
        />
      </>
    );
  }
  return (
    <>
      <TextInput label={t("mailgunDomain")} required {...form.getInputProps("domain")} />
      <Select
        label={t("region")}
        data={[
          { value: "us", label: t("us") },
          { value: "eu", label: t("eu") },
        ]}
        required
        {...form.getInputProps("region")}
      />
      <PasswordInput
        label={t("apiKey")}
        required={passwordRequired}
        autoComplete="new-password"
        {...form.getInputProps("apiKey")}
      />
    </>
  );
}

function valuesFromMailbox(mailbox: Mailbox): MailboxFormValues {
  return {
    ...initialValues,
    provider: mailbox.provider,
    host: mailbox.settings?.host || "",
    port: mailbox.settings?.port || 587,
    useSsl: mailbox.settings?.useSsl ?? true,
    username: mailbox.settings?.username || "",
    domain: mailbox.settings?.domain || "",
    region: mailbox.settings?.region || "us",
  };
}

export function MailboxesPage() {
  const { t } = useI18n("communications");
  const client = useQueryClient();
  const query = useQuery(mailboxesQueryOptions());
  const form = useForm<MailboxFormValues>({ initialValues });
  const credentialsForm = useForm<MailboxFormValues>({ initialValues });
  const [deactivatingId, setDeactivatingId] = useState<string | null>(null);
  const [credentialMailbox, setCredentialMailbox] = useState<Mailbox | null>(null);
  const [createModalOpen, setCreateModalOpen] = useState(false);

  const refresh = () => void client.invalidateQueries({ queryKey: ["mailboxes"] });
  const create = useMutation({
    mutationFn: (values: MailboxFormValues) => {
      const credentials = credentialFields(values);
      const body: CreateMailboxRequest = {
        fromAddress: values.fromAddress.trim(),
        displayName: values.displayName.trim() || undefined,
        ...credentials,
      };
      return createMailbox(body);
    },
    onSuccess: () => {
      form.reset();
      setCreateModalOpen(false);
      refresh();
      notifications.show({ title: t("mailboxCreated"), message: t("workspaceMailboxReady") });
    },
    onError: (error: ApiError) => {
      if (error.status === 409 && error.code === "mailbox_address_exists") {
        form.setFieldError("fromAddress", error.message);
        return;
      }
      notifications.show({ color: "red", title: t("mailboxNotCreated"), message: error.message });
    },
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Parameters<typeof updateMailbox>[1] }) => updateMailbox(id, body),
    onSuccess: () => {
      setDeactivatingId(null);
      refresh();
      notifications.show({ title: t("mailboxUpdated"), message: t("mailboxSettingsSaved") });
    },
    onError: (error: ApiError) =>
      notifications.show({
        color: "red",
        title: error.code === "mailbox_default_required" ? t("defaultMailboxRequired") : t("mailboxNotUpdated"),
        message: error.message,
      }),
  });
  const verify = useMutation({
    mutationFn: verifyMailbox,
    onSuccess: () => notifications.show({ title: t("mailboxVerified"), message: t("mailboxConnectionValid") }),
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: t("verificationFailed"), message: error.message }),
  });
  if (query.isPending) return <Loader />;
  if (query.isError) return <Alert color="red">{query.error.message}</Alert>;
  const mailboxes = query.data;
  return (
    <Stack gap="xl">
      <PageHeader
        eyebrow={t("communications")}
        title={t("mailboxes")}
        description={t("mailboxesDescription")}
        actions={<Button onClick={() => setCreateModalOpen(true)}>{t("addMailbox")}</Button>}
      />
      <Card withBorder radius="lg">
        <Table.ScrollContainer minWidth={950}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("fromAddress")}</Table.Th>
                <Table.Th>{t("displayName")}</Table.Th>
                <Table.Th>{t("provider")}</Table.Th>
                <Table.Th>{t("default")}</Table.Th>
                <Table.Th>{t("status")}</Table.Th>
                <Table.Th>{t("created")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {mailboxes.map((mailbox) => (
                <MailboxRow
                  key={mailbox.id}
                  mailbox={mailbox}
                  onSave={(displayName) => update.mutate({ id: mailbox.id, body: { displayName } })}
                  onToggle={() => {
                    if (mailbox.isActive) setDeactivatingId(mailbox.id);
                    else update.mutate({ id: mailbox.id, body: { isActive: true } });
                  }}
                  onSetDefault={() => update.mutate({ id: mailbox.id, body: { isDefault: true } })}
                  onVerify={() => verify.mutate(mailbox.id)}
                  onEditCredentials={() => {
                    setCredentialMailbox(mailbox);
                    credentialsForm.setValues(valuesFromMailbox(mailbox));
                    credentialsForm.resetDirty(valuesFromMailbox(mailbox));
                  }}
                />
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Card>
      {deactivatingId && (
        <Card withBorder shadow="md">
          <Stack>
            <Text fw={600}>{t("deactivateThisMailbox")}</Text>
            <Text size="sm" c="dimmed">
              {t("mailboxInactiveDescription")}
            </Text>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setDeactivatingId(null)}>
                {t("cancel")}
              </Button>
              <Button
                color="red"
                loading={update.isPending}
                onClick={() => update.mutate({ id: deactivatingId, body: { isActive: false } })}
              >
                {t("deactivate")}
              </Button>
            </Group>
          </Stack>
        </Card>
      )}
      <Modal
        opened={createModalOpen}
        onClose={() => setCreateModalOpen(false)}
        title={t("addMailboxTitle")}
        centered
        size="lg"
      >
        <form onSubmit={form.onSubmit((values) => create.mutate(values))}>
          <Stack>
            <TextInput label={t("fromAddress")} type="email" required {...form.getInputProps("fromAddress")} />
            <TextInput label={t("displayName")} {...form.getInputProps("displayName")} />
            <Select
              label={t("provider")}
              data={[
                { value: "smtp", label: t("smtp") },
                { value: "mailgun", label: t("mailgun") },
              ]}
              required
              {...form.getInputProps("provider")}
            />
            <CredentialFields form={form} passwordRequired />
            <Button type="submit" loading={create.isPending} w="fit-content">
              {t("createMailbox")}
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={credentialMailbox !== null}
        onClose={() => setCredentialMailbox(null)}
        title={t("editCredentialsTitle")}
        centered
      >
        <form
          onSubmit={credentialsForm.onSubmit((values) => {
            if (!credentialMailbox) return;
            update.mutate({ id: credentialMailbox.id, body: credentialFields(values) });
            setCredentialMailbox(null);
          })}
        >
          <Stack>
            <Select
              label={t("provider")}
              data={[
                { value: "smtp", label: t("smtp") },
                { value: "mailgun", label: t("mailgun") },
              ]}
              required
              {...credentialsForm.getInputProps("provider")}
            />
            <CredentialFields form={credentialsForm} passwordRequired />
            <Button type="submit" loading={update.isPending}>
              {t("saveCredentials")}
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}

function MailboxRow({
  mailbox,
  onSave,
  onToggle,
  onSetDefault,
  onVerify,
  onEditCredentials,
}: {
  mailbox: Mailbox;
  onSave: (displayName: string | null) => void;
  onToggle: () => void;
  onSetDefault: () => void;
  onVerify: () => void;
  onEditCredentials: () => void;
}) {
  const { t, formatters } = useI18n("communications");
  const [displayName, setDisplayName] = useState(mailbox.displayName || "");
  return (
    <Table.Tr>
      <Table.Td>{mailbox.fromAddress}</Table.Td>
      <Table.Td>
        <TextInput
          aria-label={t("displayNameFor", { address: mailbox.fromAddress })}
          value={displayName}
          onChange={(event) => setDisplayName(event.currentTarget.value)}
          onBlur={() => onSave(displayName || null)}
        />
      </Table.Td>
      <Table.Td>
        <Badge>{mailbox.provider === "smtp" ? t("smtp") : t("mailgun")}</Badge>
      </Table.Td>
      <Table.Td>{mailbox.isDefault && <Badge color="indigo">{t("default")}</Badge>}</Table.Td>
      <Table.Td>
        <Badge color={mailbox.isActive ? "green" : "gray"}>{mailbox.isActive ? t("active") : t("inactive")}</Badge>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{formatters.formatDate(mailbox.createdAt, { dateStyle: "medium", timeStyle: "short" })}</Text>
      </Table.Td>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          {!mailbox.isDefault && (
            <Button size="xs" variant="subtle" onClick={onSetDefault}>
              {t("setDefault")}
            </Button>
          )}
          <Button size="xs" variant="subtle" onClick={onToggle}>
            {mailbox.isActive ? t("deactivate") : t("activate")}
          </Button>
          <Button size="xs" variant="subtle" onClick={onVerify}>
            {t("verify")}
          </Button>
          <Button size="xs" variant="subtle" onClick={onEditCredentials}>
            {t("editCredentials")}
          </Button>
        </Group>
      </Table.Td>
    </Table.Tr>
  );
}

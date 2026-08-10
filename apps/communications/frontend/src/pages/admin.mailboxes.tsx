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
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  if (form.values.provider === "smtp") {
    return (
      <>
        <TextInput label="SMTP host" required {...form.getInputProps("host")} />
        <NumberInput label="SMTP port" min={1} max={65535} required {...form.getInputProps("port")} />
        <Checkbox label="Use SSL" {...form.getInputProps("useSsl", { type: "checkbox" })} />
        <TextInput label="Username" {...form.getInputProps("username")} />
        <PasswordInput
          label="Password"
          required={passwordRequired}
          autoComplete="new-password"
          {...form.getInputProps("password")}
        />
      </>
    );
  }
  return (
    <>
      <TextInput label="Mailgun domain" required {...form.getInputProps("domain")} />
      <Select
        label="Region"
        data={[
          { value: "us", label: "US" },
          { value: "eu", label: "EU" },
        ]}
        required
        {...form.getInputProps("region")}
      />
      <PasswordInput
        label="API key"
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
      notifications.show({ title: "Mailbox created", message: "The workspace mailbox is ready." });
    },
    onError: (error: ApiError) => {
      if (error.status === 409 && error.code === "mailbox_address_exists") {
        form.setFieldError("fromAddress", error.message);
        return;
      }
      notifications.show({ color: "red", title: "Mailbox not created", message: error.message });
    },
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Parameters<typeof updateMailbox>[1] }) => updateMailbox(id, body),
    onSuccess: () => {
      setDeactivatingId(null);
      refresh();
      notifications.show({ title: "Mailbox updated", message: "Mailbox settings saved." });
    },
    onError: (error: ApiError) =>
      notifications.show({
        color: "red",
        title: error.code === "mailbox_default_required" ? "Default mailbox required" : "Mailbox not updated",
        message: error.message,
      }),
  });
  const verify = useMutation({
    mutationFn: verifyMailbox,
    onSuccess: () => notifications.show({ title: "Mailbox verified", message: "The mailbox connection is valid." }),
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: "Verification failed", message: error.message }),
  });
  if (query.isPending) return <Loader />;
  if (query.isError) return <Alert color="red">{query.error.message}</Alert>;
  const mailboxes = query.data;
  return (
    <Stack gap="xl">
      <Group justify="space-between" align="end">
        <div>
          <Text className="eyebrow">Administration</Text>
          <Title order={2}>Mailboxes</Title>
          <Text c="dimmed">Configure the workspace sending mailboxes and credentials.</Text>
        </div>
        <Button onClick={() => setCreateModalOpen(true)}>Add Mailbox</Button>
      </Group>
      <Card withBorder radius="lg">
        <Table.ScrollContainer minWidth={950}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>From address</Table.Th>
                <Table.Th>Display name</Table.Th>
                <Table.Th>Provider</Table.Th>
                <Table.Th>Default</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Created</Table.Th>
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
            <Text fw={600}>Deactivate this mailbox?</Text>
            <Text size="sm" c="dimmed">
              Messages cannot be sent from this mailbox while it is inactive.
            </Text>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setDeactivatingId(null)}>
                Cancel
              </Button>
              <Button
                color="red"
                loading={update.isPending}
                onClick={() => update.mutate({ id: deactivatingId, body: { isActive: false } })}
              >
                Deactivate
              </Button>
            </Group>
          </Stack>
        </Card>
      )}
      <Modal opened={createModalOpen} onClose={() => setCreateModalOpen(false)} title="Add mailbox" centered size="lg">
        <form onSubmit={form.onSubmit((values) => create.mutate(values))}>
          <Stack>
            <TextInput label="From address" type="email" required {...form.getInputProps("fromAddress")} />
            <TextInput label="Display name" {...form.getInputProps("displayName")} />
            <Select
              label="Provider"
              data={[
                { value: "smtp", label: "SMTP" },
                { value: "mailgun", label: "Mailgun" },
              ]}
              required
              {...form.getInputProps("provider")}
            />
            <CredentialFields form={form} passwordRequired />
            <Button type="submit" loading={create.isPending} w="fit-content">
              Create mailbox
            </Button>
          </Stack>
        </form>
      </Modal>
      <Modal
        opened={credentialMailbox !== null}
        onClose={() => setCredentialMailbox(null)}
        title="Edit credentials"
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
              label="Provider"
              data={[
                { value: "smtp", label: "SMTP" },
                { value: "mailgun", label: "Mailgun" },
              ]}
              required
              {...credentialsForm.getInputProps("provider")}
            />
            <CredentialFields form={credentialsForm} passwordRequired />
            <Button type="submit" loading={update.isPending}>
              Save credentials
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
  const [displayName, setDisplayName] = useState(mailbox.displayName || "");
  return (
    <Table.Tr>
      <Table.Td>{mailbox.fromAddress}</Table.Td>
      <Table.Td>
        <TextInput
          aria-label={`Display name for ${mailbox.fromAddress}`}
          value={displayName}
          onChange={(event) => setDisplayName(event.currentTarget.value)}
          onBlur={() => onSave(displayName || null)}
        />
      </Table.Td>
      <Table.Td>
        <Badge>{mailbox.provider === "smtp" ? "SMTP" : "Mailgun"}</Badge>
      </Table.Td>
      <Table.Td>{mailbox.isDefault && <Badge color="indigo">Default</Badge>}</Table.Td>
      <Table.Td>
        <Badge color={mailbox.isActive ? "green" : "gray"}>{mailbox.isActive ? "Active" : "Inactive"}</Badge>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{new Date(mailbox.createdAt).toLocaleString()}</Text>
      </Table.Td>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          {!mailbox.isDefault && (
            <Button size="xs" variant="subtle" onClick={onSetDefault}>
              Set default
            </Button>
          )}
          <Button size="xs" variant="subtle" onClick={onToggle}>
            {mailbox.isActive ? "Deactivate" : "Activate"}
          </Button>
          <Button size="xs" variant="subtle" onClick={onVerify}>
            Verify
          </Button>
          <Button size="xs" variant="subtle" onClick={onEditCredentials}>
            Edit credentials
          </Button>
        </Group>
      </Table.Td>
    </Table.Tr>
  );
}

import { Alert, Button, Modal, PasswordInput, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import {
  type ChannelProvider,
  type ChannelProviderKey,
  type CreateChannelRequest,
  createChannel,
} from "../api/channels";
import { channelErrorMessage } from "./channelErrors";

type ChannelFormValues = {
  address: string;
  name: string;
  provider: ChannelProviderKey;
  smtpHost: string;
  smtpPort: string;
  smtpUsername: string;
  smtpPassword: string;
};

export type ChannelFormProps = {
  opened: boolean;
  onClose: () => void;
  onCreated: () => void;
  /** GET /channel-providers: Microsoft 365 is offered only while it answers `entra` available. */
  providers: ChannelProvider[];
  providerLabel: (key: string) => string;
};

const initialValues: ChannelFormValues = {
  address: "",
  name: "",
  provider: "smtp",
  smtpHost: "",
  smtpPort: "587",
  smtpUsername: "",
  smtpPassword: "",
};

export function ChannelForm({ opened, onClose, onCreated, providers, providerLabel }: ChannelFormProps) {
  const { t, locale } = useI18n("communications");
  const form = useForm<ChannelFormValues>({ initialValues });
  const entraAvailable = providers.some((provider) => provider.key === "entra" && provider.available);
  const provider = form.values.provider === "entra" && !entraAvailable ? "smtp" : form.values.provider;
  const create = useMutation({
    mutationFn: (values: ChannelFormValues): Promise<unknown> => {
      const body: CreateChannelRequest = {
        type: "email",
        address: values.address.trim(),
        displayName: values.name.trim() || undefined,
        provider,
        ...(provider === "smtp"
          ? {
              smtp: {
                host: values.smtpHost.trim(),
                port: Number(values.smtpPort),
                useSsl: true,
                username: values.smtpUsername.trim() || undefined,
                password: values.smtpPassword || undefined,
              },
            }
          : {}),
      };
      return createChannel(body);
    },
    onSuccess: () => {
      form.setValues(initialValues);
      form.resetDirty();
      onClose();
      onCreated();
    },
  });
  const close = () => {
    create.reset();
    onClose();
  };

  return (
    <Modal opened={opened} onClose={close} title={t("addEmailChannel")}>
      <form onSubmit={form.onSubmit((values) => create.mutate(values))}>
        <Stack>
          <TextInput required type="email" label={t("emailAddress")} {...form.getInputProps("address")} />
          <TextInput label={t("displayName")} {...form.getInputProps("name")} />
          <Select
            label={t("provider")}
            allowDeselect={false}
            {...form.getInputProps("provider")}
            value={provider}
            onChange={(value) => {
              if (value) form.setFieldValue("provider", value as ChannelProviderKey);
              create.reset();
            }}
            data={[
              { value: "smtp", label: providerLabel("smtp") },
              ...(entraAvailable ? [{ value: "entra", label: providerLabel("entra") }] : []),
            ]}
          />
          {provider === "smtp" ? (
            <>
              <TextInput required label={t("smtpHost")} {...form.getInputProps("smtpHost")} />
              <TextInput required label={t("smtpPort")} {...form.getInputProps("smtpPort")} />
              <TextInput label={t("username")} {...form.getInputProps("smtpUsername")} />
              <PasswordInput label={t("password")} {...form.getInputProps("smtpPassword")} />
            </>
          ) : (
            <Text size="sm" c="dimmed">
              {t("sharedMailboxOnly")}
            </Text>
          )}
          {create.error && <Alert color="red">{channelErrorMessage(create.error, t, provider, locale)}</Alert>}
          <Button type="submit" loading={create.isPending}>
            {t("createChannel")}
          </Button>
        </Stack>
      </form>
    </Modal>
  );
}

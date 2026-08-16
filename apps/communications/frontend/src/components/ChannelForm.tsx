import { Button, Modal, PasswordInput, Select, Stack, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type CreateChannelRequest, createChannel } from "../api/channels";

type ChannelFormValues = {
  address: string;
  name: string;
  provider: "smtp" | "mailgun";
  smtpHost: string;
  smtpPort: string;
  smtpUsername: string;
  smtpPassword: string;
  mailgunDomain: string;
  mailgunRegion: "us" | "eu";
  mailgunApiKey: string;
  inboundSigningKey: string;
};

export type ChannelFormProps = {
  opened: boolean;
  onClose: () => void;
  onCreated: () => void;
};

const initialValues: ChannelFormValues = {
  address: "",
  name: "",
  provider: "smtp",
  smtpHost: "",
  smtpPort: "587",
  smtpUsername: "",
  smtpPassword: "",
  mailgunDomain: "",
  mailgunRegion: "us",
  mailgunApiKey: "",
  inboundSigningKey: "",
};

export function ChannelForm({ opened, onClose, onCreated }: ChannelFormProps) {
  const { t } = useI18n("communications");
  const form = useForm<ChannelFormValues>({ initialValues });
  const create = useMutation({
    mutationFn: (values: ChannelFormValues): Promise<unknown> => {
      const body: CreateChannelRequest = {
        type: "email",
        address: values.address.trim(),
        displayName: values.name.trim() || undefined,
        provider: values.provider,
        ...(values.provider === "smtp"
          ? {
              smtp: {
                host: values.smtpHost.trim(),
                port: Number(values.smtpPort),
                useSsl: true,
                username: values.smtpUsername.trim() || undefined,
                password: values.smtpPassword || undefined,
              },
            }
          : {
              mailgun: {
                domain: values.mailgunDomain.trim(),
                region: values.mailgunRegion,
                apiKey: values.mailgunApiKey,
                inboundSigningKey: values.inboundSigningKey,
              },
            }),
      };
      return createChannel(body);
    },
    onSuccess: () => {
      const mailgunRegion = form.values.mailgunRegion;
      form.setValues({ ...initialValues, mailgunRegion });
      form.resetDirty();
      onClose();
      onCreated();
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("addEmailChannel")}>
      <form onSubmit={form.onSubmit((values) => create.mutate(values))}>
        <Stack>
          <TextInput required type="email" label={t("emailAddress")} {...form.getInputProps("address")} />
          <TextInput label={t("displayName")} {...form.getInputProps("name")} />
          <Select
            label={t("provider")}
            {...form.getInputProps("provider")}
            data={[
              { value: "smtp", label: t("smtp") },
              { value: "mailgun", label: t("mailgun") },
            ]}
          />
          {form.values.provider === "smtp" ? (
            <>
              <TextInput required label={t("smtpHost")} {...form.getInputProps("smtpHost")} />
              <TextInput required label={t("smtpPort")} {...form.getInputProps("smtpPort")} />
              <TextInput label={t("username")} {...form.getInputProps("smtpUsername")} />
              <PasswordInput label={t("password")} {...form.getInputProps("smtpPassword")} />
            </>
          ) : (
            <>
              <TextInput required label={t("mailgunDomain")} {...form.getInputProps("mailgunDomain")} />
              <Select
                required
                label={t("region")}
                {...form.getInputProps("mailgunRegion")}
                data={[
                  { value: "us", label: t("us") },
                  { value: "eu", label: t("eu") },
                ]}
              />
              <PasswordInput required label={t("apiKey")} {...form.getInputProps("mailgunApiKey")} />
              <PasswordInput required label={t("inboundSigningKey")} {...form.getInputProps("inboundSigningKey")} />
            </>
          )}
          <Button type="submit" loading={create.isPending}>
            {t("createChannel")}
          </Button>
        </Stack>
      </form>
    </Modal>
  );
}

import {
  Alert,
  Button,
  Group,
  Modal,
  PasswordInput,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import {
  type ChannelImportWindow,
  type ChannelOnClose,
  type ChannelProvider,
  type ChannelProviderKey,
  type ChannelType,
  type CreateChannelRequest,
  createChannel,
} from "../api/channels";
import { channelErrorMessage } from "./channelErrors";
import { isMailboxProvider, mailboxProviders, messengerProviders, providerText } from "./channelProviders";

type ChannelFormValues = {
  /** The channel's type, chosen first (Messenger design N8). */
  type: ChannelType;
  /** A Messenger channel's provider and the Page picked from its endpoints. */
  messengerProvider: string;
  page: string;
  address: string;
  name: string;
  provider: ChannelProviderKey;
  smtpHost: string;
  smtpPort: string;
  smtpUsername: string;
  smtpPassword: string;
  importWindow: ChannelImportWindow;
  onClose: ChannelOnClose;
  folder: string;
};

export type ChannelFormProps = {
  opened: boolean;
  onClose: () => void;
  onCreated: () => void;
  /**
   * GET /channel-providers: SMTP, then every mailbox provider in the order the
   * server lists them; a provider not available now is listed, disabled, with
   * its reason.
   */
  providers: ChannelProvider[];
  providerLabel: (key: string) => string;
};

/**
 * The entry for Vantigo hosted mail: listed so the Owner sees it is coming, never
 * selectable, never submitted. It is not a `ChannelProviderKey` and the API does not
 * know it.
 */
const hostedOption = "hosted";

const initialValues: ChannelFormValues = {
  type: "email",
  messengerProvider: "",
  page: "",
  address: "",
  name: "",
  provider: "smtp",
  smtpHost: "",
  smtpPort: "587",
  smtpUsername: "",
  smtpPassword: "",
  importWindow: "from_now",
  onClose: "none",
  folder: "Handled in Vantigo",
};

export function ChannelForm({ opened, onClose, onCreated, providers, providerLabel }: ChannelFormProps) {
  const { t, locale } = useI18n("communications");
  const form = useForm<ChannelFormValues>({ initialValues });
  const emailProviders = mailboxProviders(providers);
  const selectable = (key: string) =>
    !isMailboxProvider(key) || emailProviders.some((p) => p.key === key && p.available);
  // The Messenger type is offered only while a messenger provider is available (design N8): the
  // Pages it has connected are what the picker lists.
  const chatProviders = messengerProviders(providers).filter((p) => p.available);
  const chatOffered = chatProviders.length > 0;
  const isChat = chatOffered && form.values.type === "messenger";
  const chatProvider =
    chatProviders.find((p) => p.key === form.values.messengerProvider) ?? chatProviders[0] ?? undefined;
  const pages = chatProvider?.endpoints ?? [];
  const page = pages.find((p) => p.address === form.values.page && p.available);
  const provider = selectable(form.values.provider) ? form.values.provider : "smtp";
  const providerName = providerLabel(isChat ? (chatProvider?.key ?? provider) : provider);
  // A listed provider that cannot be picked now says so, with the reason
  // the server answered (switch_off, status_unavailable, …) beside it.
  const unavailableReason = (p: ChannelProvider | undefined) =>
    p && !p.available ? t("providerUnavailable", { reason: p.reason ?? "" }) : "";
  const create = useMutation({
    mutationFn: (values: ChannelFormValues): Promise<unknown> => {
      if (isChat && chatProvider)
        return createChannel({
          type: "messenger",
          address: values.page,
          displayName: values.name.trim() || undefined,
          provider: chatProvider.key,
        });
      const body: CreateChannelRequest = {
        type: "email",
        address: values.address.trim(),
        displayName: values.name.trim() || undefined,
        provider,
        ...(!isMailboxProvider(provider)
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
              ...(values.importWindow !== "from_now" ? { importWindow: values.importWindow } : {}),
              ...(values.onClose !== "none" ? { onClose: values.onClose } : {}),
              ...(values.onClose === "move" && values.folder.trim() ? { folder: values.folder.trim() } : {}),
            }),
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
    <Modal opened={opened} onClose={close} title={isChat ? t("addMessengerChannel") : t("addEmailChannel")}>
      <form onSubmit={form.onSubmit((values) => create.mutate(values))}>
        <Stack>
          {chatOffered && (
            <SegmentedControl
              fullWidth
              aria-label={t("channelType")}
              value={form.values.type}
              onChange={(value) => {
                form.setFieldValue("type", value as ChannelType);
                create.reset();
              }}
              data={[
                { value: "email", label: t("channelEmail") },
                { value: "messenger", label: t("channelMessenger") },
              ]}
            />
          )}
          {isChat ? (
            <>
              {chatProviders.length > 1 && (
                <Select
                  label={t("provider")}
                  allowDeselect={false}
                  value={chatProvider?.key ?? null}
                  onChange={(value) => {
                    if (value) {
                      form.setFieldValue("messengerProvider", value);
                      form.setFieldValue("page", "");
                    }
                  }}
                  data={chatProviders.map((p) => ({ value: p.key, label: p.displayName }))}
                />
              )}
              <Select
                required
                label={t("facebookPage")}
                placeholder={pages.length === 0 ? t("noPagesConnected") : undefined}
                allowDeselect={false}
                value={page?.address ?? null}
                onChange={(value) => value && form.setFieldValue("page", value)}
                data={pages.map((p) => ({
                  value: p.address,
                  label: p.displayName || p.address,
                  disabled: !p.available,
                }))}
                renderOption={({ option }) => {
                  const endpoint = pages.find((p) => p.address === option.value);
                  return (
                    <Group justify="space-between" wrap="nowrap" w="100%">
                      <Text size="sm">{option.label}</Text>
                      {endpoint && !endpoint.available && (
                        <Text size="xs" c="dimmed">
                          {t("providerUnavailable", { reason: endpoint.reason ?? "" })}
                        </Text>
                      )}
                    </Group>
                  );
                }}
              />
              <TextInput label={t("displayName")} {...form.getInputProps("name")} />
            </>
          ) : (
            <>
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
                  ...emailProviders.map((p) => ({ value: p.key, label: p.displayName, disabled: !p.available })),
                  { value: hostedOption, label: t("providerHosted"), disabled: true },
                ]}
                renderOption={({ option }) => {
                  const hint =
                    option.value === hostedOption
                      ? t("providerHostedHint")
                      : unavailableReason(emailProviders.find((p) => p.key === option.value));
                  return hint ? (
                    <Group justify="space-between" wrap="nowrap" w="100%">
                      <Text size="sm">{option.label}</Text>
                      <Text size="xs" c="dimmed">
                        {hint}
                      </Text>
                    </Group>
                  ) : (
                    <Text size="sm">{option.label}</Text>
                  );
                }}
              />
              {!isMailboxProvider(provider) ? (
                <>
                  <TextInput required label={t("smtpHost")} {...form.getInputProps("smtpHost")} />
                  <TextInput required label={t("smtpPort")} {...form.getInputProps("smtpPort")} />
                  <TextInput label={t("username")} {...form.getInputProps("smtpUsername")} />
                  <PasswordInput label={t("password")} {...form.getInputProps("smtpPassword")} />
                </>
              ) : (
                <>
                  <Text size="sm" c="dimmed">
                    {t(providerText("sharedMailboxOnly", provider), { provider: providerName })}
                  </Text>
                  <Select
                    label={t("importWindow")}
                    allowDeselect={false}
                    value={form.values.importWindow}
                    onChange={(value) => {
                      if (value) form.setFieldValue("importWindow", value as ChannelImportWindow);
                    }}
                    data={[
                      { value: "from_now", label: t("importFromNow") },
                      { value: "last_30_days", label: t("importLast30Days") },
                      { value: "last_90_days", label: t("importLast90Days") },
                    ]}
                  />
                  <Select
                    label={t("onClose")}
                    allowDeselect={false}
                    value={form.values.onClose}
                    onChange={(value) => {
                      if (value) form.setFieldValue("onClose", value as ChannelOnClose);
                    }}
                    data={[
                      { value: "none", label: t("onCloseNone") },
                      { value: "read", label: t("onCloseRead") },
                      { value: "move", label: t("onCloseMove") },
                    ]}
                  />
                  {form.values.onClose === "move" && (
                    <TextInput required label={t("onCloseFolder")} {...form.getInputProps("folder")} />
                  )}
                </>
              )}
            </>
          )}
          {create.error && (
            <Alert color="red">{channelErrorMessage(create.error, t, locale, undefined, providerName, provider)}</Alert>
          )}
          <Button type="submit" loading={create.isPending} disabled={isChat && !page}>
            {t("createChannel")}
          </Button>
        </Stack>
      </form>
    </Modal>
  );
}

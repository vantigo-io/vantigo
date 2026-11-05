import {
  Alert,
  Button,
  Card,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Tabs,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDeviceFloppy } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { metaQueryOptions } from "../api/meta";
import { ApiValidationError, isVersionConflict } from "../api/request";
import { settingsQueryOptions, updateSettings } from "../api/settings";
import type { MailText, Settings } from "../api/types";
import { ConflictAlert } from "../components/conflict-alert";
import { WarningsCard } from "../components/warnings-card";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

const mailKinds = ["quote", "confirmation"] as const;
const languages = ["nb", "en"] as const;

/**
 * Orders' settings (§9, O12, O25): where the quote and order series start,
 * how long a quote is valid and the default payment terms; the channel
 * documents are sent from, whether a confirmation is mailed on confirm and
 * whether orders are combined on one invoice by default; and the mail texts
 * per kind and language, empty for the built-in ones.
 */
export const SettingsPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const settings = useQuery({ ...settingsQueryOptions(), refetchOnWindowFocus: false });
  const meta = useQuery(metaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("settingsTitle")} description={t("settingsDescription")} />
      {settings.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(settings.error, t)}
        </Alert>
      )}
      {settings.data && <SettingsForm key={settings.data.revision} settings={settings.data} access={access} />}
      {settings.data && <WarningsCard access={access} currency={meta.data?.baseCurrency ?? "NOK"} />}
    </Stack>
  );
};

/** The form's values: the settings, with a number input allowed to stand empty while it is typed in. */
type FormValues = { [K in keyof Settings]: Settings[K] extends number ? number | "" : Settings[K] };

const mailTextOf = (texts: readonly MailText[], kind: MailText["kind"], language: MailText["language"]): MailText =>
  texts.find((text) => text.kind === kind && text.language === language) ?? { kind, language, subject: "", body: "" };

const SettingsForm = ({ settings, access }: { settings: Settings; access: OrdersAccess }) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const manage = access.canManageSettings;
  const [values, setValues] = useState<FormValues>(settings);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);
  const [dirty, setDirty] = useState(false);
  const set = <K extends keyof FormValues>(key: K, value: FormValues[K]) => {
    setValues((current) => ({ ...current, [key]: value }));
    setErrors((current) => ({ ...current, [key]: "" }));
    setDirty(true);
  };
  const setMailText = (kind: MailText["kind"], language: MailText["language"], patch: Partial<MailText>) => {
    const others = values.mailTexts.filter((text) => !(text.kind === kind && text.language === language));
    set("mailTexts", [...others, { ...mailTextOf(values.mailTexts, kind, language), ...patch }]);
  };
  const save = useMutation({
    mutationFn: () =>
      updateSettings({
        revision: settings.revision,
        // A number left empty is sent as its least, which the server checks as any other.
        quoteSeriesStart: Number(values.quoteSeriesStart) || 1,
        orderSeriesStart: Number(values.orderSeriesStart) || 1,
        quoteValidityDays: Number(values.quoteValidityDays) || 1,
        defaultPaymentTermsDays: Number(values.defaultPaymentTermsDays) || 0,
        confirmationOnConfirm: values.confirmationOnConfirm,
        combineByDefault: values.combineByDefault,
        sendChannelId: values.sendChannelId ?? null,
        // An override left empty falls back to the built-in text (O25), so it is not sent.
        mailTexts: values.mailTexts.filter((text) => text.subject.trim() !== "" || text.body.trim() !== ""),
      }),
    onSuccess: (saved) => {
      queryClient.setQueryData(settingsQueryOptions().queryKey, saved);
      notifications.show({ color: "green", message: t("settingsSaved") });
    },
    onError: (error) => {
      if (isVersionConflict(error)) return setConflict(true);
      if (error instanceof ApiValidationError)
        setErrors(
          Object.fromEntries(Object.entries(error.errors).map(([field, messages]) => [field, messages[0] ?? ""])),
        );
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t) });
    },
  });
  // The channels come with the settings, as Communications answers them (O25).
  const channelOptions = settings.sendChannels.map((channel) => ({
    value: channel.id,
    label: channel.displayName ? `${channel.displayName} <${channel.address}>` : channel.address,
    disabled: channel.available === false && channel.id !== values.sendChannelId,
  }));
  if (values.sendChannelId && !channelOptions.some((option) => option.value === values.sendChannelId))
    channelOptions.unshift({ value: values.sendChannelId, label: t("channelUnknown"), disabled: false });

  return (
    <Stack gap="lg">
      {conflict && (
        <ConflictAlert
          onReload={() => void queryClient.refetchQueries({ queryKey: settingsQueryOptions().queryKey })}
        />
      )}
      <Card withBorder padding="lg">
        <Stack>
          <Title order={4}>{t("numberSeries")}</Title>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("quoteSeriesStart")}
              description={t("nextNumber", { number: settings.nextQuoteNumber })}
              min={1}
              allowDecimal={false}
              disabled={!manage}
              value={values.quoteSeriesStart}
              error={errors.quoteSeriesStart}
              onChange={(value) => set("quoteSeriesStart", value === "" ? "" : Number(value))}
            />
            <NumberInput
              label={t("orderSeriesStart")}
              description={t("nextNumber", { number: settings.nextOrderNumber })}
              min={1}
              allowDecimal={false}
              disabled={!manage}
              value={values.orderSeriesStart}
              error={errors.orderSeriesStart}
              onChange={(value) => set("orderSeriesStart", value === "" ? "" : Number(value))}
            />
          </SimpleGrid>
          <Text size="xs" c="dimmed">
            {t("seriesHint")}
          </Text>
        </Stack>
      </Card>
      <Card withBorder padding="lg">
        <Stack>
          <Title order={4}>{t("defaults")}</Title>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <NumberInput
              label={t("quoteValidity")}
              suffix={` ${t("days")}`}
              min={1}
              max={365}
              allowDecimal={false}
              disabled={!manage}
              value={values.quoteValidityDays}
              error={errors.quoteValidityDays}
              onChange={(value) => set("quoteValidityDays", value === "" ? "" : Number(value))}
            />
            <NumberInput
              label={t("defaultPaymentTerms")}
              description={t("defaultPaymentTermsHint")}
              suffix={` ${t("days")}`}
              min={0}
              max={365}
              allowDecimal={false}
              disabled={!manage}
              value={values.defaultPaymentTermsDays}
              error={errors.defaultPaymentTermsDays}
              onChange={(value) => set("defaultPaymentTermsDays", value === "" ? "" : Number(value))}
            />
          </SimpleGrid>
          <Switch
            label={t("combineByDefault")}
            description={t("combineByDefaultHint")}
            disabled={!manage}
            checked={values.combineByDefault}
            onChange={(e) => set("combineByDefault", e.currentTarget.checked)}
          />
        </Stack>
      </Card>
      <Card withBorder padding="lg" data-testid="sending-card">
        <Stack>
          <Title order={4}>{t("sending")}</Title>
          {!settings.sendingAvailable && (
            <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
              {t("sendingOffSettings")}
            </Alert>
          )}
          <Select
            label={t("sendChannel")}
            description={t("sendChannelHint")}
            placeholder={t("sendChannelNone")}
            data={channelOptions}
            clearable
            disabled={!manage}
            value={values.sendChannelId ?? null}
            error={errors.sendChannelId}
            onChange={(value) => set("sendChannelId", value)}
          />
          <Switch
            label={t("confirmationOnConfirm")}
            description={t("confirmationOnConfirmHint")}
            disabled={!manage}
            checked={values.confirmationOnConfirm}
            onChange={(e) => set("confirmationOnConfirm", e.currentTarget.checked)}
          />
        </Stack>
      </Card>
      <Card withBorder padding="lg">
        <Stack>
          <Title order={4}>{t("mailTexts")}</Title>
          <Text size="sm" c="dimmed">
            {t("mailTextsHint")}
          </Text>
          <Tabs defaultValue="quote">
            <Tabs.List>
              {mailKinds.map((kind) => (
                <Tabs.Tab key={kind} value={kind}>
                  {t(`mailKind.${kind}`)}
                </Tabs.Tab>
              ))}
            </Tabs.List>
            {mailKinds.map((kind) => (
              <Tabs.Panel key={kind} value={kind} pt="md">
                <SimpleGrid cols={{ base: 1, md: 2 }}>
                  {languages.map((language) => {
                    const text = mailTextOf(values.mailTexts, kind, language);
                    return (
                      <Stack key={language} gap="xs">
                        <Text size="sm" fw={600}>
                          {language === "nb" ? t("languageNb") : t("languageEn")}
                        </Text>
                        <TextInput
                          label={t("mailSubject")}
                          placeholder={t("builtInText")}
                          maxLength={300}
                          disabled={!manage}
                          value={text.subject}
                          onChange={(e) => setMailText(kind, language, { subject: e.currentTarget.value })}
                        />
                        <Textarea
                          label={t("mailBody")}
                          placeholder={t("builtInText")}
                          autosize
                          minRows={4}
                          maxLength={4000}
                          disabled={!manage}
                          value={text.body}
                          onChange={(e) => setMailText(kind, language, { body: e.currentTarget.value })}
                        />
                      </Stack>
                    );
                  })}
                </SimpleGrid>
              </Tabs.Panel>
            ))}
          </Tabs>
        </Stack>
      </Card>
      {manage && (
        <Group justify="flex-end">
          <Button
            leftSection={<IconDeviceFloppy size={16} />}
            disabled={!dirty}
            loading={save.isPending}
            onClick={() => save.mutate()}
            data-assist="orders.settings.save"
          >
            {t("save")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

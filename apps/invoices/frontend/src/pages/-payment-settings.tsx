import {
  Alert,
  Button,
  Card,
  Checkbox,
  Group,
  List,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import {
  type PaymentSettings,
  type PaymentSettingsInput,
  paymentSettingsQueryOptions,
  putPaymentSettings,
} from "../api/payment-settings";
import { ApiConflictError, ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/**
 * The payment settings (invoices payments and reminders design D14, D17),
 * read from the server: the form is drawn once they are known.
 */
export const PaymentSettingsCard = () => {
  const { t, date } = useInvoiceFormat();
  const stored = useQuery(paymentSettingsQueryOptions());
  // A save lost to someone else's: said here, outside the form, which is
  // drawn afresh on the newer revision — the person's edits are not kept, and
  // the notice says so — until the next save is answered.
  const [stale, setStale] = useState(false);
  if (stored.isError && !stored.data) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("paymentSettings.failedToLoad")}>
        {refusalMessage(stored.error, t, date)}
      </Alert>
    );
  }
  if (!stored.data) return <ContentSkeleton rows={3} rowHeight={36} />;
  return (
    <Stack gap="xs">
      {stale && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />} withCloseButton onClose={() => setStale(false)}>
          {t("paymentSettings.stale")}
        </Alert>
      )}
      <PaymentSettingsForm
        key={stored.data.revision}
        stored={stored.data}
        onSaving={() => setStale(false)}
        onStale={() => setStale(true)}
      />
    </Stack>
  );
};

/** The kontantsalg notice's paragraphs after its title, in order: its one wording (open question 3). */
const noticeParagraphs = ["cashSale", "withoutRegister", "counterSale", "truthfulBuyer", "payLinkOnSite"] as const;

/**
 * The two switches, the two addresses pay links need, and the kontantsalg
 * notice (D17 rule 3) in full — shown whenever either switch is on in the
 * form, since a pay link shown on site is the same cash sale (I21). Turning
 * payment on site on needs the acknowledgement box, which is sent as
 * `acknowledgeKontantsalg`; once on, who acknowledged it and when is said
 * instead. A switch on in the settings that meta says is not available is
 * explained — `PUBLIC_BASE_URL` among the reasons for pay links — and every
 * refusal is said in words.
 */
const PaymentSettingsForm = ({
  stored,
  onSaving,
  onStale,
}: {
  stored: PaymentSettings;
  /** A save is sent: any earlier lost save is no longer news. */
  onSaving: () => void;
  /** The save named a stale revision: the card says so, and the form is read again. */
  onStale: () => void;
}) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [payLinks, setPayLinks] = useState(stored.payLinksEnabled);
  const [onSite, setOnSite] = useState(stored.onSitePaymentsEnabled);
  const [termsUrl, setTermsUrl] = useState(stored.termsUrl ?? "");
  const [privacyUrl, setPrivacyUrl] = useState(stored.privacyUrl ?? "");
  const [acknowledged, setAcknowledged] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refused, setRefused] = useState<string | null>(null);
  // Turning payment on site on — not keeping it on — is what the notice is acknowledged for.
  const turningOnSiteOn = onSite && !stored.onSitePaymentsEnabled;
  const save = useMutation({
    mutationFn: () => {
      const input: PaymentSettingsInput = {
        payLinksEnabled: payLinks,
        termsUrl: termsUrl.trim() || null,
        privacyUrl: privacyUrl.trim() || null,
        onSitePaymentsEnabled: onSite,
        revision: stored.revision,
      };
      if (turningOnSiteOn) input.acknowledgeKontantsalg = true;
      return putPaymentSettings(input);
    },
    onMutate: () => {
      setRefused(null);
      onSaving();
    },
    onSuccess: async () => {
      setErrors({});
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("paymentSettings.saved") });
    },
    onError: async (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => ["termsUrl", "privacyUrl", "acknowledgeKontantsalg"].includes(field),
          "paymentSettings",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefused(elsewhere.join(" "));
        return;
      }
      // A stale revision carries no code: the settings are read again.
      if (error instanceof ApiConflictError && !refusalCode(error)) {
        onStale();
        await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "payment-settings"] });
        return;
      }
      setRefused(refusalMessage(error, t, date));
    },
  });
  const submit = () => {
    if (turningOnSiteOn && !acknowledged) {
      setErrors((current) => ({ ...current, acknowledgeKontantsalg: t("paymentSettings.acknowledgeRequired") }));
      return;
    }
    save.mutate();
  };
  const unsetError = (field: string) =>
    setErrors((current) => {
      const next = { ...current };
      delete next[field];
      return next;
    });
  return (
    <Card withBorder data-testid="payment-settings">
      <Stack>
        <Title order={4}>{t("paymentSettings.title")}</Title>
        <Switch
          label={t("paymentSettings.payLinks")}
          description={t("paymentSettings.payLinksHint")}
          checked={payLinks}
          onChange={(e) => setPayLinks(e.currentTarget.checked)}
        />
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("paymentSettings.termsUrl")}
            description={t("paymentSettings.urlHint")}
            type="url"
            maxLength={500}
            error={errors.termsUrl}
            value={termsUrl}
            onChange={(e) => {
              setTermsUrl(e.currentTarget.value);
              unsetError("termsUrl");
            }}
          />
          <TextInput
            label={t("paymentSettings.privacyUrl")}
            description={t("paymentSettings.urlHint")}
            type="url"
            maxLength={500}
            error={errors.privacyUrl}
            value={privacyUrl}
            onChange={(e) => {
              setPrivacyUrl(e.currentTarget.value);
              unsetError("privacyUrl");
            }}
          />
        </SimpleGrid>
        {stored.payLinksEnabled && meta.data && !meta.data.payLinksAvailable && (
          <Alert color="yellow" icon={<IconInfoCircle size={16} />}>
            {t("paymentSettings.payLinksNotAvailable")}
          </Alert>
        )}
        <Switch
          label={t("paymentSettings.onSite")}
          description={t("paymentSettings.onSiteHint")}
          checked={onSite}
          onChange={(e) => {
            setOnSite(e.currentTarget.checked);
            unsetError("acknowledgeKontantsalg");
          }}
        />
        {stored.onSitePaymentsEnabled && meta.data && !meta.data.onSitePaymentsAvailable && (
          <Alert color="yellow" icon={<IconInfoCircle size={16} />}>
            {t("paymentSettings.onSiteNotAvailable")}
          </Alert>
        )}
        {(payLinks || onSite) && (
          <Alert
            color="orange"
            icon={<IconAlertTriangle size={16} />}
            title={t("paymentSettings.notice.title")}
            data-testid="kontantsalg-notice"
          >
            <List size="sm" spacing="xs">
              {noticeParagraphs.map((paragraph) => (
                <List.Item key={paragraph}>{t(`paymentSettings.notice.${paragraph}`)}</List.Item>
              ))}
            </List>
          </Alert>
        )}
        {turningOnSiteOn && (
          <Checkbox
            label={t("paymentSettings.acknowledge")}
            checked={acknowledged}
            error={errors.acknowledgeKontantsalg}
            onChange={(e) => {
              setAcknowledged(e.currentTarget.checked);
              unsetError("acknowledgeKontantsalg");
            }}
          />
        )}
        {onSite && stored.onSitePaymentsEnabled && stored.kontantsalgAcknowledgedAt && (
          <Text size="sm" c="dimmed">
            {t("paymentSettings.acknowledgedOn", { at: dateTime(stored.kontantsalgAcknowledgedAt) })}
          </Text>
        )}
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("paymentSettings.couldNotSave")}>
            {refused}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button loading={save.isPending} onClick={submit}>
            {t("paymentSettings.save")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

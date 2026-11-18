import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AvtaleGiroMandate,
  type ConsumerProvider,
  getConsumerProvider,
  listMandates,
  putConsumerProvider,
  resolveMandate,
  setAvtaleGiro,
  verifyConsumerProvider,
} from "../api/efaktura";
import { INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const providerKey = [INVOICES_QUERY_KEY, "consumer-provider"] as const;
const mandatesKey = [INVOICES_QUERY_KEY, "avtalegiro-mandates"] as const;
const agreements = ["none", "pending", "active"] as const;
const verifyColours: Record<string, string> = { ok: "green", unauthorized: "red", unreachable: "yellow" };

/**
 * The eFaktura and AvtaleGiro section (invoices phase 6 design I29, I32,
 * I35): the tenant's agreements with its bank, the creditor account and the
 * names the bank shows; Verify; the AvtaleGiro switch, which the server
 * refuses while the KID has no room for the customer KIDs; the count sent per
 * month; and the mandates whose KID names no customer, to assign.
 */
export const EfakturaSettingsCard = () => {
  const { t, date } = useInvoiceFormat();
  const stored = useQuery({ queryKey: providerKey, queryFn: getConsumerProvider });
  if (stored.isError && !stored.data) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("efaktura.failedToLoad")}>
        {refusalMessage(stored.error, t, date)}
      </Alert>
    );
  }
  if (!stored.data) return <ContentSkeleton rows={3} rowHeight={36} />;
  return <EfakturaForm stored={stored.data} />;
};

const EfakturaForm = ({ stored }: { stored: ConsumerProvider }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [efakturaAgreement, setEfakturaAgreement] = useState(stored.efakturaAgreement ?? "none");
  const [avtalegiroAgreement, setAvtalegiroAgreement] = useState(stored.avtalegiroAgreement ?? "none");
  const [creditorAccount, setCreditorAccount] = useState(stored.creditorAccount ?? "");
  const [brandName, setBrandName] = useState(stored.brandName ?? "");
  const [abbreviatedName, setAbbreviatedName] = useState(stored.abbreviatedName ?? "");
  const [refused, setRefused] = useState<string | null>(null);
  const [verified, setVerified] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const save = useMutation({
    mutationFn: () =>
      putConsumerProvider({
        provider: "mps",
        efakturaAgreement,
        avtalegiroAgreement,
        brandName,
        creditorAccount: creditorAccount || undefined,
        abbreviatedName: abbreviatedName || undefined,
      }),
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      await refresh();
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  const verify = useMutation({
    mutationFn: verifyConsumerProvider,
    onSuccess: async (result) => {
      setVerified(result.result);
      await refresh();
    },
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  const avtalegiro = useMutation({
    mutationFn: (enabled: boolean) => setAvtaleGiro(enabled),
    onMutate: () => setRefused(null),
    onSuccess: refresh,
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  const options = agreements.map((value) => ({ value, label: t(`efaktura.agreement.${value}`) }));
  return (
    <PageSection>
      <Stack gap="sm" data-testid="efaktura-settings" data-assist="invoices.settings.efaktura">
        <Group justify="space-between">
          <Title order={3}>{t("efaktura.settingsTitle")}</Title>
          {verified && (
            <Badge color={verifyColours[verified]} variant="light">
              {t(`efaktura.verify.${verified}`)}
            </Badge>
          )}
        </Group>
        <Text size="sm">{t("efaktura.settingsHelp")}</Text>
        {!stored.operatorReady && (
          <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
            {t("efaktura.operatorNotReady")}
          </Alert>
        )}
        {stored.rejectedAt && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {t("efaktura.rejected")}
          </Alert>
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Select
            label={t("efaktura.efakturaAgreement")}
            data={options}
            value={efakturaAgreement}
            onChange={(v) => setEfakturaAgreement(v ?? "none")}
            allowDeselect={false}
          />
          <Select
            label={t("efaktura.avtalegiroAgreement")}
            data={options}
            value={avtalegiroAgreement}
            onChange={(v) => setAvtalegiroAgreement(v ?? "none")}
            allowDeselect={false}
          />
          <TextInput
            label={t("efaktura.brandName")}
            value={brandName}
            maxLength={40}
            onChange={(e) => setBrandName(e.currentTarget.value)}
          />
          <TextInput
            label={t("efaktura.abbreviatedName")}
            value={abbreviatedName}
            maxLength={10}
            onChange={(e) => setAbbreviatedName(e.currentTarget.value)}
          />
          <TextInput
            label={t("efaktura.creditorAccount")}
            value={creditorAccount}
            onChange={(e) => setCreditorAccount(e.currentTarget.value)}
          />
        </SimpleGrid>
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />}>
            {refused}
          </Alert>
        )}
        <Group>
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
          {stored.hasCredentials && (
            <Button variant="default" loading={verify.isPending} onClick={() => verify.mutate()}>
              {t("efaktura.verifyButton")}
            </Button>
          )}
        </Group>
        {stored.hasCredentials && (
          <Switch
            label={t("efaktura.avtalegiroSwitch")}
            description={t("efaktura.avtalegiroSwitchHelp")}
            checked={stored.avtalegiroEnabled}
            disabled={avtalegiro.isPending}
            onChange={(e) => avtalegiro.mutate(e.currentTarget.checked)}
          />
        )}
        {stored.sentPerMonth.length > 0 && (
          <Stack gap={2}>
            <Text size="sm" fw={600}>
              {t("efaktura.sentPerMonth")}
            </Text>
            {stored.sentPerMonth.map((m) => (
              <Text size="sm" key={m.month}>
                {t("efaktura.monthCount", { month: m.month, count: m.count })}
              </Text>
            ))}
          </Stack>
        )}
        {stored.avtalegiroEnabled && <UnresolvedMandates />}
      </Stack>
    </PageSection>
  );
};

/** The mandates whose KID names no customer, each with Assign to customer. */
export const UnresolvedMandates = () => {
  const { t } = useInvoiceFormat();
  const mandates = useQuery({ queryKey: [...mandatesKey, "unresolved"], queryFn: () => listMandates(true) });
  const items = mandates.data?.items ?? [];
  return (
    <Stack gap={4} data-testid="unresolved-mandates" data-assist="invoices.avtalegiro.mandates">
      <Text size="sm" fw={600}>
        {t("efaktura.unresolvedMandates")}
      </Text>
      {items.length === 0 ? (
        <Text size="sm">{t("efaktura.noUnresolvedMandates")}</Text>
      ) : (
        <Table>
          <Table.Tbody>
            {items.map((m) => (
              <MandateRow key={m.id} mandate={m} />
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  );
};

const MandateRow = ({ mandate }: { mandate: AvtaleGiroMandate }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [customerId, setCustomerId] = useState<number | string>("");
  const [mismatch, setMismatch] = useState(false);
  const [reason, setReason] = useState("");
  const assign = useMutation({
    mutationFn: () =>
      resolveMandate(mandate.id, Number(customerId), mismatch ? { confirmMismatch: true, reason } : undefined),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("efaktura.mandateAssigned") });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("efaktura.couldNotAssign"),
        message: refusalMessage(error, t, date),
      }),
  });
  return (
    <Table.Tr>
      <Table.Td>{mandate.kid}</Table.Td>
      <Table.Td>{t(`efaktura.mandateStatus.${mandate.status}`)}</Table.Td>
      <Table.Td>
        <Group gap="xs">
          <NumberInput
            aria-label={t("efaktura.customerId")}
            placeholder={t("efaktura.customerId")}
            value={customerId}
            onChange={setCustomerId}
            min={1}
            w={120}
          />
          <Checkbox
            label={t("efaktura.confirmMismatch")}
            checked={mismatch}
            onChange={(e) => setMismatch(e.currentTarget.checked)}
          />
          {mismatch && (
            <TextInput
              aria-label={t("efaktura.mismatchReason")}
              placeholder={t("efaktura.mismatchReason")}
              value={reason}
              maxLength={500}
              onChange={(e) => setReason(e.currentTarget.value)}
            />
          )}
          <Button
            size="xs"
            disabled={!customerId || (mismatch && !reason.trim())}
            loading={assign.isPending}
            onClick={() => assign.mutate()}
          >
            {t("efaktura.assign")}
          </Button>
        </Group>
      </Table.Td>
    </Table.Tr>
  );
};

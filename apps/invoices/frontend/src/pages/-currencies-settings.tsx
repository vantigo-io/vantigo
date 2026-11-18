import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Group,
  MultiSelect,
  NumberInput,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  exchangeRatesQueryOptions,
  fetchRatesNow,
  NORGES_BANK_CURRENCIES,
  putManualRate,
  removeManualRate,
} from "../api/exchange-rates";
import { INVOICES_QUERY_KEY } from "../api/request";
import { type InvoiceSettings, invoiceSettingsQueryOptions, updateInvoiceSettings } from "../api/settings";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/**
 * The settings as PUT takes them, from what GET answered: the card changes
 * the currencies alone and sends the rest as it stands.
 */
const asInput = (s: InvoiceSettings, currencies: string[]) => ({
  legalName: s.legalName,
  organisationNumber: s.organisationNumber,
  vatRegistered: s.vatRegistered,
  inForetaksregisteret: s.inForetaksregisteret,
  addressLine1: s.addressLine1,
  addressLine2: s.addressLine2,
  postalCode: s.postalCode,
  city: s.city,
  country: s.country,
  bankAccount: s.bankAccount,
  iban: s.iban,
  bic: s.bic,
  email: s.email,
  phone: s.phone,
  defaultPaymentTermsDays: s.defaultPaymentTermsDays,
  tolerance: s.tolerance,
  defaultCurrency: s.defaultCurrency,
  footerText: s.footerText,
  seriesStart: s.seriesStart,
  peppolId: s.peppolId ?? null,
  kidLength: s.kidLength ?? null,
  kidAlgorithm: s.kidAlgorithm ?? null,
  workVatCodes: s.workVatCodes,
  timesheetDefault: s.timesheetDefault,
  timesheetPersonLabel: s.timesheetPersonLabel,
  energyVatCodes: s.energyVatCodes,
  currencies,
  revision: s.revision,
});

/**
 * The currencies a document may be in besides NOK (invoices phase 6 design
 * I17, I18): turned on from Norges Bank's list — which needs the IBAN and the
 * BIC and a rate within 7 days — each with its last fetch, Fetch now, its
 * last rates and a manual rate, which a fetch never overwrites.
 */
export const CurrenciesSettingsCard = ({ canManage }: { canManage: boolean }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const settings = useQuery(invoiceSettingsQueryOptions());
  const [refused, setRefused] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const save = useMutation({
    mutationFn: (currencies: string[]) => {
      if (!settings.data) throw new Error("settings not loaded");
      return updateInvoiceSettings(asInput(settings.data, currencies));
    },
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      await refresh();
      notifications.show({ color: "green", message: t("currencies.saved") });
    },
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  const fetchNow = useMutation({
    mutationFn: fetchRatesNow,
    onMutate: () => setRefused(null),
    onSuccess: refresh,
    onError: (error) => setRefused(refusalMessage(error, t, date)),
  });
  if (!settings.data) return <ContentSkeleton rows={2} rowHeight={36} />;
  const s = settings.data;
  return (
    <PageSection data-testid="currencies-settings">
      <Stack>
        <Title order={4}>{t("currencies.title")}</Title>
        <Text size="sm" c="dimmed">
          {t("currencies.hint")}
        </Text>
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("currencies.couldNotSave")}>
            {refused}
          </Alert>
        )}
        <MultiSelect
          data-assist="invoices.settings.currencies"
          label={t("currencies.label")}
          data={[...NORGES_BANK_CURRENCIES]}
          value={s.currencies}
          searchable
          disabled={!canManage || save.isPending}
          onChange={(next) => save.mutate(next)}
        />
        {s.currencies.length > 0 && (
          <Group>
            <Button
              variant="light"
              loading={fetchNow.isPending}
              disabled={!canManage}
              onClick={() => fetchNow.mutate()}
            >
              {t("currencies.fetchNow")}
            </Button>
          </Group>
        )}
        {s.currencies.map((currency) => {
          const fetch = s.rateFetches.find((f) => f.currency === currency);
          return (
            <Stack key={currency} gap="xs">
              <Group gap="xs">
                <Text fw={600}>{currency}</Text>
                {fetch?.lastError ? (
                  <Badge color="red" variant="light">
                    {t("currencies.lastError", { error: fetch.lastError })}
                  </Badge>
                ) : fetch?.lastSuccessAt ? (
                  <Badge color="green" variant="light">
                    {t("currencies.lastFetch", { at: dateTime(fetch.lastSuccessAt) })}
                  </Badge>
                ) : null}
              </Group>
              <CurrencyRates currency={currency} canManage={canManage} />
            </Stack>
          );
        })}
      </Stack>
    </PageSection>
  );
};

/** A currency's last seven rates, and a manual rate entered or removed. */
const CurrencyRates = ({ currency, canManage }: { currency: string; canManage: boolean }) => {
  const { t, date, number } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const rates = useQuery(exchangeRatesQueryOptions(currency));
  const [day, setDay] = useState("");
  const [rate, setRate] = useState<number | string>("");
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const enter = useMutation({
    mutationFn: () => putManualRate(currency, day, Number(rate)),
    onSuccess: async () => {
      setDay("");
      setRate("");
      await refresh();
    },
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(error, t, date) }),
  });
  const remove = useMutation({
    mutationFn: (d: string) => removeManualRate(currency, d),
    onSuccess: refresh,
    onError: (error) => notifications.show({ color: "red", message: refusalMessage(error, t, date) }),
  });
  return (
    <Stack gap="xs">
      <Table>
        <Table.Tbody>
          {(rates.data?.items ?? []).slice(0, 7).map((r) => (
            <Table.Tr key={r.day}>
              <Table.Td>{date(r.day)}</Table.Td>
              <Table.Td>{number(r.rate, 6)}</Table.Td>
              <Table.Td>{t(`currencies.source.${r.source}`)}</Table.Td>
              <Table.Td>
                {canManage && r.source === "manual" && (
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label={t("currencies.remove")}
                    onClick={() => remove.mutate(r.day)}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                )}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {canManage && (
        <Group align="end">
          <TextInput
            label={t("currencies.day")}
            type="date"
            value={day}
            onChange={(e) => setDay(e.currentTarget.value)}
          />
          <NumberInput label={t("currencies.rate")} decimalScale={6} min={0} value={rate} onChange={setRate} />
          <Button variant="light" disabled={!day || !rate} loading={enter.isPending} onClick={() => enter.mutate()}>
            {t("currencies.enterRate")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

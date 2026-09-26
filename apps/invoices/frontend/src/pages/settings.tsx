import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  List,
  Modal,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCheck, IconLock, IconPencil, IconPlus, IconTrash, IconX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { type ChangeEvent, useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import { INVOICES_QUERY_KEY } from "../api/request";
import { type InvoiceSettings, invoiceSettingsQueryOptions, updateInvoiceSettings } from "../api/settings";
import {
  addVatCodeRate,
  createVatCode,
  deleteVatCodeRate,
  updateVatCode,
  type VatCode,
  vatCodesQueryOptions,
} from "../api/vat-codes";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/** The seller fields issuing needs, in the form's order (D2). */
const requiredSellerFields = [
  "legalName",
  "organisationNumber",
  "addressLine1",
  "postalCode",
  "city",
  "bankAccount",
] as const;

const categories = ["S", "Z", "E", "AE", "G", "O", "K"];

/**
 * Invoice settings (D12), `invoices:manage`'s: the seller record with the
 * completeness checklist, the series start read-only once anything is issued,
 * and the VAT codes with their rate periods. The host guards the route with
 * the permission; the page asks meta's `canManage` too, so a caller whose
 * access changed under it is told why rather than shown a form every save of
 * which is refused.
 */
export const SettingsPage = () => {
  const { t, date } = useInvoiceFormat();
  const meta = useQuery(invoicesMetaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("invoiceSettings")} description={t("invoiceSettingsDescription")} />
      {meta.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(meta.error, t, date)}
        </Alert>
      )}
      {meta.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {meta.data &&
        (meta.data.capabilities.canManage ? (
          <Settings />
        ) : (
          <Alert color="gray" icon={<IconLock size={16} />}>
            {t("settingsNeedManage")}
          </Alert>
        ))}
    </Stack>
  );
};

const Settings = () => {
  const { t, date } = useInvoiceFormat();
  const settings = useQuery(invoiceSettingsQueryOptions());
  return (
    <>
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(settings.error, t, date)}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {settings.data && <SellerForm key={settings.data.revision} settings={settings.data} />}
      <VatCodesSection />
    </>
  );
};

const SellerForm = ({ settings }: { settings: InvoiceSettings }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [values, setValues] = useState(settings);
  const set = <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K]) =>
    setValues((v) => ({ ...v, [key]: value }));
  const save = useMutation({
    mutationFn: () =>
      updateInvoiceSettings({
        legalName: values.legalName,
        organisationNumber: values.organisationNumber,
        vatRegistered: values.vatRegistered,
        inForetaksregisteret: values.inForetaksregisteret,
        addressLine1: values.addressLine1,
        addressLine2: values.addressLine2,
        postalCode: values.postalCode,
        city: values.city,
        country: values.country,
        bankAccount: values.bankAccount,
        iban: values.iban,
        bic: values.bic,
        email: values.email,
        defaultPaymentTermsDays: values.defaultPaymentTermsDays,
        defaultCurrency: values.defaultCurrency,
        footerText: values.footerText,
        seriesStart: values.seriesStart,
        revision: settings.revision,
      }),
    onSuccess: async (saved) => {
      queryClient.setQueryData(invoiceSettingsQueryOptions().queryKey, saved);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: refusalMessage(error, t, date) }),
  });
  const text = (
    key:
      | "legalName"
      | "organisationNumber"
      | "addressLine1"
      | "addressLine2"
      | "postalCode"
      | "city"
      | "country"
      | "bankAccount"
      | "iban"
      | "bic"
      | "email",
  ) => ({
    label: t(`field.${key}`),
    value: values[key],
    onChange: (e: ChangeEvent<HTMLInputElement>) => set(key, e.currentTarget.value),
  });
  return (
    <Card withBorder>
      <Stack>
        <Title order={4}>{t("seller")}</Title>
        <List spacing={4} size="sm" aria-label={t("completeness")}>
          {requiredSellerFields.map((field) => {
            const missing = settings.missingSellerFields.includes(field);
            return (
              <List.Item
                key={field}
                icon={missing ? <IconX size={14} color="red" /> : <IconCheck size={14} color="green" />}
              >
                {missing ? t("missingField", { field: t(`field.${field}`) }) : t(`field.${field}`)}
              </List.Item>
            );
          })}
        </List>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput {...text("legalName")} />
          <TextInput {...text("organisationNumber")} />
          <TextInput {...text("addressLine1")} />
          <TextInput {...text("addressLine2")} />
          <TextInput {...text("postalCode")} />
          <TextInput {...text("city")} />
          <TextInput {...text("country")} />
          <TextInput {...text("email")} />
          <TextInput {...text("bankAccount")} />
          <TextInput {...text("iban")} />
          <TextInput {...text("bic")} />
          <NumberInput
            label={t("field.defaultPaymentTermsDays")}
            min={0}
            max={365}
            value={values.defaultPaymentTermsDays}
            onChange={(v) => set("defaultPaymentTermsDays", typeof v === "number" ? v : Number(v) || 0)}
          />
        </SimpleGrid>
        <Group>
          <Checkbox
            label={t("field.vatRegistered")}
            checked={values.vatRegistered}
            onChange={(e) => set("vatRegistered", e.currentTarget.checked)}
          />
          <Checkbox
            label={t("field.inForetaksregisteret")}
            checked={values.inForetaksregisteret}
            onChange={(e) => set("inForetaksregisteret", e.currentTarget.checked)}
          />
        </Group>
        <Textarea
          label={t("field.footerText")}
          value={values.footerText}
          onChange={(e) => set("footerText", e.currentTarget.value)}
        />
        <NumberInput
          label={t("field.seriesStart")}
          description={settings.seriesLocked ? t("seriesLocked") : t("seriesStartHint")}
          disabled={settings.seriesLocked}
          min={1}
          value={values.seriesStart}
          onChange={(v) => set("seriesStart", typeof v === "number" ? v : Number(v) || 1)}
        />
        <Group justify="flex-end">
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

type CodeModal = { mode: "create" } | { mode: "edit"; code: VatCode } | { mode: "rates"; code: VatCode } | null;

const VatCodesSection = () => {
  const { t, number, date } = useInvoiceFormat();
  const codes = useQuery(vatCodesQueryOptions());
  const meta = useQuery(invoicesMetaQueryOptions());
  const [modal, setModal] = useState<CodeModal>(null);
  const today = meta.data?.today ?? "";
  const current = (code: VatCode) => code.rates.find((r) => r.validFrom <= today && (!r.validTo || r.validTo >= today));
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("vatCodes")}</Title>
          <Button leftSection={<IconPlus size={16} />} variant="light" onClick={() => setModal({ mode: "create" })}>
            {t("addVatCode")}
          </Button>
        </Group>
        {codes.isPending && <ContentSkeleton rows={4} rowHeight={36} />}
        {codes.data && (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("code")}</Table.Th>
                <Table.Th>{t("name")}</Table.Th>
                <Table.Th>{t("category")}</Table.Th>
                <Table.Th>{t("safTCode")}</Table.Th>
                <Table.Th ta="right">{t("rateToday")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {codes.data.map((code) => {
                const rate = current(code);
                return (
                  <Table.Tr key={code.id}>
                    <Table.Td>{code.code}</Table.Td>
                    <Table.Td>
                      {code.name} {!code.active && <Badge color="gray">{t("inactive")}</Badge>}
                    </Table.Td>
                    <Table.Td>{code.ehfCategory}</Table.Td>
                    <Table.Td>{code.safTCode}</Table.Td>
                    <Table.Td ta="right">{rate ? `${number(rate.ratePercent, 2)} %` : t("notAvailable")}</Table.Td>
                    <Table.Td>
                      <Group gap={4} justify="flex-end" wrap="nowrap">
                        <Button size="xs" variant="subtle" onClick={() => setModal({ mode: "rates", code })}>
                          {t("ratePeriods")}
                        </Button>
                        <ActionIcon
                          variant="subtle"
                          aria-label={t("editVatCode", { code: code.code })}
                          onClick={() => setModal({ mode: "edit", code })}
                        >
                          <IconPencil size={16} />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
      {modal?.mode === "create" && <VatCodeForm today={today} onClose={() => setModal(null)} />}
      {modal?.mode === "edit" && <VatCodeForm code={modal.code} today={today} onClose={() => setModal(null)} />}
      {modal?.mode === "rates" && (
        <RatePeriods
          code={codes.data?.find((c) => c.id === modal.code.id) ?? modal.code}
          today={today}
          date={date}
          onClose={() => setModal(null)}
        />
      )}
    </Card>
  );
};

/** A new code's first period starts today in Oslo unless the person picks another day. */
const VatCodeForm = ({ code, today, onClose }: { code?: VatCode; today: string; onClose: () => void }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [values, setValues] = useState({
    code: code?.code ?? "",
    name: code?.name ?? "",
    safTCode: code?.safTCode ?? "",
    ehfCategory: code?.ehfCategory ?? "S",
    exemptionReason: code?.exemptionReason ?? "",
    active: code?.active ?? true,
    ratePercent: 25 as number | string,
    validFrom: (today || null) as string | null,
  });
  const save = useMutation({
    mutationFn: () => {
      const own = {
        code: values.code,
        name: values.name,
        safTCode: values.safTCode,
        ehfCategory: values.ehfCategory,
        ...(values.exemptionReason ? { exemptionReason: values.exemptionReason } : {}),
      };
      return code
        ? updateVatCode(code.id, { ...own, active: values.active, revision: code.revision })
        : createVatCode({ ...own, ratePercent: Number(values.ratePercent) || 0, validFrom: values.validFrom ?? "" });
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      onClose();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSaveVatCode"), message: refusalMessage(error, t, date) }),
  });
  return (
    <Modal opened onClose={onClose} title={code ? t("editVatCode", { code: code.code }) : t("addVatCode")}>
      <Stack>
        <TextInput
          label={t("code")}
          value={values.code}
          onChange={(e) => setValues({ ...values, code: e.currentTarget.value })}
        />
        <TextInput
          label={t("name")}
          value={values.name}
          onChange={(e) => setValues({ ...values, name: e.currentTarget.value })}
        />
        <TextInput
          label={t("safTCode")}
          disabled={code?.inUse}
          value={values.safTCode}
          onChange={(e) => setValues({ ...values, safTCode: e.currentTarget.value })}
        />
        <Select
          label={t("category")}
          disabled={code?.inUse}
          data={categories}
          value={values.ehfCategory}
          onChange={(v) => setValues({ ...values, ehfCategory: v ?? "S" })}
        />
        {code?.inUse && (
          <Text size="sm" c="dimmed">
            {t("vatCodeInUseHint")}
          </Text>
        )}
        <TextInput
          label={t("exemptionReason")}
          value={values.exemptionReason}
          onChange={(e) => setValues({ ...values, exemptionReason: e.currentTarget.value })}
        />
        {code ? (
          <Checkbox
            label={t("activeCode")}
            checked={values.active}
            onChange={(e) => setValues({ ...values, active: e.currentTarget.checked })}
          />
        ) : (
          <Group grow>
            <NumberInput
              label={t("ratePercent")}
              decimalScale={2}
              value={values.ratePercent}
              onChange={(v) => setValues({ ...values, ratePercent: v })}
            />
            <DateInput
              label={t("validFrom")}
              valueFormat={t("dateInputFormat")}
              value={values.validFrom}
              onChange={(v) => setValues({ ...values, validFrom: v })}
            />
          </Group>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

/**
 * A code's rate periods (D3): every period, adding one from a date — the open
 * one closes the day before — and removing the latest, while it lies in the
 * future.
 */
const RatePeriods = ({
  code,
  today,
  date,
  onClose,
}: {
  code: VatCode;
  today: string;
  date: (d: string) => string;
  onClose: () => void;
}) => {
  const { t, number } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [ratePercent, setRatePercent] = useState<number | string>(code.ehfCategory === "S" ? 25 : 0);
  const [validFrom, setValidFrom] = useState<string | null>(null);
  const done = async () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const fail = (error: Error) =>
    notifications.show({ color: "red", title: t("couldNotChangeRate"), message: refusalMessage(error, t, date) });
  const add = useMutation({
    mutationFn: () => addVatCodeRate(code.id, Number(ratePercent) || 0, validFrom ?? ""),
    onSuccess: done,
    onError: fail,
  });
  const remove = useMutation({
    mutationFn: (rateId: number) => deleteVatCodeRate(code.id, rateId),
    onSuccess: done,
    onError: fail,
  });
  const latest = code.rates[code.rates.length - 1];
  return (
    <Modal opened onClose={onClose} title={t("ratePeriodsOf", { code: code.code })}>
      <Stack>
        <Table>
          <Table.Tbody>
            {code.rates.map((r) => (
              <Table.Tr key={r.id}>
                <Table.Td>{`${number(r.ratePercent, 2)} %`}</Table.Td>
                <Table.Td>
                  {r.validTo
                    ? t("periodClosed", { from: date(r.validFrom), to: date(r.validTo) })
                    : t("periodOpen", { from: date(r.validFrom) })}
                </Table.Td>
                <Table.Td>
                  {r.id === latest?.id && code.rates.length > 1 && r.validFrom > today && (
                    <ActionIcon
                      color="red"
                      variant="subtle"
                      aria-label={t("removePeriod")}
                      onClick={() => remove.mutate(r.id)}
                    >
                      <IconTrash size={16} />
                    </ActionIcon>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Group grow align="flex-end">
          <NumberInput label={t("newRate")} decimalScale={2} value={ratePercent} onChange={setRatePercent} />
          <DateInput
            label={t("validFrom")}
            valueFormat={t("dateInputFormat")}
            value={validFrom}
            onChange={setValidFrom}
          />
        </Group>
        <Group justify="flex-end">
          <Button disabled={!validFrom} loading={add.isPending} onClick={() => add.mutate()}>
            {t("addPeriod")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

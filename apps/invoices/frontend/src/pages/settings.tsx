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
  PasswordInput,
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
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconCheck,
  IconInfoCircle,
  IconLock,
  IconPencil,
  IconPlus,
  IconTrash,
  IconX,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { type ChangeEvent, useState } from "react";
import {
  type AccessPoint,
  accessPointQueryOptions,
  deleteAccessPoint,
  putAccessPoint,
  verifyAccessPoint,
} from "../api/access-point";
import { type InvoicesMeta, invoicesMetaQueryOptions } from "../api/meta";
import { ApiConflictError, ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { type InvoiceSettings, invoiceSettingsQueryOptions, updateInvoiceSettings } from "../api/settings";
import {
  addVatCodeRate,
  createVatCode,
  deleteVatCodeRate,
  updateVatCode,
  type VatCode,
  vatCodesQueryOptions,
} from "../api/vat-codes";
import { StaleAlert } from "../components/stale-alert";
import { invoicesCatalog } from "../i18n";
import { fieldRefusals, refusalCode, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { computeKid, kidFits } from "../lib/kid";
import { rateOn } from "../lib/vat";

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

/** The Peppol id the server derives from an organisation number when none is set (EHF and KID design D2). */
const derivedPeppolId = (organisationNumber: string): string | null =>
  organisationNumber ? `0192:${organisationNumber}` : null;

/** The rate a new code or period starts at: 25 % for S, the standard rate; 0 % for every other category. */
const defaultRate = (category: string): number => (category === "S" ? 25 : 0);

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
  // The settings the unsaved edits were made on. While there are any, the form
  // keeps them rather than remounting on a newer revision a refetch brings in:
  // the person's edits are never thrown away unasked, and the form says the
  // settings changed — the draft editor's rule.
  const [editedFrom, setEditedFrom] = useState<InvoiceSettings | null>(null);
  const shown = editedFrom ?? settings.data;
  return (
    <>
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(settings.error, t, date)}
        </Alert>
      )}
      {settings.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {shown && settings.data && (
        <SellerForm
          key={shown.revision}
          settings={shown}
          latestRevision={settings.data.revision}
          dirty={shown === editedFrom}
          onDirtyChange={(dirty) => setEditedFrom((current) => (dirty ? (current ?? shown) : null))}
        />
      )}
      <VatCodesSection />
    </>
  );
};

/** The seller fields that have an input, which a 400 naming them is shown on. */
const sellerInputs = new Set([
  "legalName",
  "organisationNumber",
  "addressLine1",
  "addressLine2",
  "postalCode",
  "city",
  "country",
  "email",
  "bankAccount",
  "iban",
  "bic",
  "defaultPaymentTermsDays",
  "footerText",
  "seriesStart",
  "peppolId",
  "kidLength",
  "kidAlgorithm",
  "workVatCodes.hours",
  "workVatCodes.expenses",
  "workVatCodes.milestones",
  "timesheetDefault",
  "timesheetPersonLabel",
]);

interface SellerFormProps {
  settings: InvoiceSettings;
  /** The revision the server last answered: newer than the form's when someone else saved meanwhile. */
  latestRevision: number;
  /** Whether there are unsaved edits; the page holds it, so a refetch does not remount the form under them. */
  dirty: boolean;
  onDirtyChange: (dirty: boolean) => void;
}

/**
 * The settings form: the seller record and the series start, the seller's
 * Peppol id on the E-invoicing card, and the KID agreement on its own card —
 * one revision, one Save (EHF and KID design D2, D3, D15). A 400 puts each
 * field's refusal on its own input, worded by the catalog — every seller
 * field has one rule, so its words say what the server checked — and only a
 * refusal no input shows is a notification. A save refused as stale, or a
 * newer revision seen while editing, says the settings changed and offers
 * Reload. The access point beside the Peppol id is read and saved on its own.
 */
const SellerForm = ({ settings, latestRevision, dirty, onDirtyChange: setDirty }: SellerFormProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  // The page has read meta before it drew this form; the mail line comes from it.
  const meta = useQuery(invoicesMetaQueryOptions());
  // A Peppol id that is only the one derived from the organisation number is
  // shown as the placeholder, not as a value: the input holds an id typed in.
  const [values, setValues] = useState<InvoiceSettings>(() => ({
    ...settings,
    peppolId: settings.peppolId === derivedPeppolId(settings.organisationNumber) ? null : settings.peppolId,
  }));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);
  const [reloadFailed, setReloadFailed] = useState(false);
  const stale = conflict || latestRevision > settings.revision;
  // `field` is the refusal the change answers, when it is not the key itself
  // — one work VAT code of the three, `workVatCodes.hours`.
  const set = <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K], field: string = key) => {
    setValues((v) => ({ ...v, [key]: value }));
    setErrors((current) => {
      const next = { ...current };
      delete next[field];
      return next;
    });
    setDirty(true);
  };
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
        // The server requires all three, null included. An empty Peppol id
        // — the derived one is only the placeholder — goes as null, so the
        // server derives it again from the number saved with it: echoed, it
        // would be refused once the number changed.
        peppolId: values.peppolId?.trim() || null,
        kidLength: values.kidLength,
        kidAlgorithm: values.kidAlgorithm,
        // The Work to invoice card's: the code each kind of work is invoiced
        // at (invoices work design D6), and the timesheet's default and
        // person label (D5) — all required by the server.
        workVatCodes: values.workVatCodes,
        timesheetDefault: values.timesheetDefault,
        timesheetPersonLabel: values.timesheetPersonLabel,
        revision: settings.revision,
      }),
    onSuccess: async (saved) => {
      queryClient.setQueryData(invoiceSettingsQueryOptions().queryKey, saved);
      // The saved revision replaces the edited one: the form remounts on it.
      setDirty(false);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => sellerInputs.has(field));
        setErrors(onInputs);
        if (elsewhere.length === 0) return;
        notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: elsewhere.join(" ") });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveSettings"), message: refusalMessage(error, t, date) });
    },
  });
  // Reload drops the unsaved edits for the latest revision, which the form
  // then remounts on.
  const reload = useMutation({
    mutationFn: () => queryClient.fetchQuery({ ...invoiceSettingsQueryOptions(), staleTime: 0 }),
    onMutate: () => setReloadFailed(false),
    onSuccess: () => setDirty(false),
    onError: () => setReloadFailed(true),
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
    error: errors[key],
    onChange: (e: ChangeEvent<HTMLInputElement>) => set(key, e.currentTarget.value),
  });
  return (
    <Stack gap="lg">
      {stale && (
        <StaleAlert
          title={t("settingsChangedTitle")}
          message={t("settingsChangedMessage")}
          reloadFailedMessage={reloadFailed ? t("couldNotReloadSettings") : undefined}
          reloading={reload.isPending}
          onReload={() => reload.mutate()}
        />
      )}
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
            {/* Informative only: sending needs SMTP, issuing never does (payments and delivery design D10). */}
            {meta.data && (
              <List.Item
                data-informative="true"
                icon={
                  meta.data.mailAvailable ? (
                    <IconCheck size={14} color="green" />
                  ) : (
                    <IconInfoCircle size={14} color="gray" />
                  )
                }
              >
                {meta.data.mailAvailable ? t("mailConfigured") : t("mailNotConfigured")}
              </List.Item>
            )}
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
              error={errors.defaultPaymentTermsDays}
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
            error={errors.footerText}
            value={values.footerText}
            onChange={(e) => set("footerText", e.currentTarget.value)}
          />
          <NumberInput
            label={t("field.seriesStart")}
            description={settings.seriesLocked ? t("seriesLocked") : t("seriesStartHint")}
            disabled={settings.seriesLocked}
            min={1}
            error={errors.seriesStart}
            value={values.seriesStart}
            onChange={(v) => set("seriesStart", typeof v === "number" ? v : Number(v) || 1)}
          />
        </Stack>
      </Card>
      <Card withBorder data-testid="e-invoicing-card">
        <Stack>
          <Title order={4}>{t("eInvoicing")}</Title>
          <Text size="sm" c="dimmed">
            {t("eInvoicingDescription")}
          </Text>
          {/* The seller's Peppol id is e-invoicing's, never issuing's: its own line (D2, D15). */}
          <List spacing={4} size="sm" aria-label={t("eInvoicingReadiness")}>
            <List.Item
              icon={settings.peppolId ? <IconCheck size={14} color="green" /> : <IconX size={14} color="red" />}
            >
              {settings.peppolId ? t("peppolIdReady", { peppolId: settings.peppolId }) : t("peppolIdMissing")}
            </List.Item>
            {meta.data && (
              <List.Item
                data-informative="true"
                icon={
                  meta.data.ehfAvailable ? (
                    <IconCheck size={14} color="green" />
                  ) : (
                    <IconInfoCircle size={14} color="gray" />
                  )
                }
              >
                {meta.data.ehfAvailable ? t("ehfIsAvailable") : t("ehfIsNotAvailable")}
              </List.Item>
            )}
          </List>
          <TextInput
            label={t("field.peppolId")}
            description={t("peppolIdHint")}
            placeholder={derivedPeppolId(values.organisationNumber) ?? undefined}
            maxLength={60}
            value={values.peppolId ?? ""}
            error={errors.peppolId}
            onChange={(e) => set("peppolId", e.currentTarget.value)}
          />
          {meta.data && <AccessPointSection meta={meta.data} />}
        </Stack>
      </Card>
      <KidCard settings={settings} values={values} errors={errors} set={set} />
      <WorkCard stored={settings.workVatCodes} values={values} errors={errors} set={set} />
      <Group justify="flex-end">
        <Button loading={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
          {t("save")}
        </Button>
      </Group>
    </Stack>
  );
};

interface KidCardProps {
  /** The settings as saved: the agreement issued invoices were computed under, and the save's warnings. */
  settings: InvoiceSettings;
  /** The form's values, the agreement being edited among them. */
  values: InvoiceSettings;
  errors: Record<string, string>;
  set: <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K]) => void;
}

/**
 * The KID agreement (EHF and KID design D3, D15): the length and the check
 * digit the bank agreed, with their help; the next KID previewed — computed
 * here by the server's own MOD10 and MOD11, from the series start being
 * edited before the first issue and from the server's next number after it —
 * or, when the next number does not fit, why; the headroom warning, judged
 * live while the agreement is edited and as the save answered it otherwise;
 * and, once invoices are issued under an agreement, that changing it leaves
 * their KIDs as they were.
 */
const KidCard = ({ settings, values, errors, set }: KidCardProps) => {
  const { t } = useInvoiceFormat();
  const { kidLength: length, kidAlgorithm: algorithm } = values;
  const agreed = length !== null && algorithm !== null;
  // Before the first issue the next number is the start being edited; after
  // it, the server's.
  const next = settings.seriesLocked ? settings.nextNumber : values.seriesStart;
  // The saved warning describes the saved agreement and number; while either
  // is edited, the headroom is judged here by the server's rule.
  const edited =
    length !== settings.kidLength || algorithm !== settings.kidAlgorithm || values.seriesStart !== settings.seriesStart;
  const warnings = edited
    ? agreed && kidFits(next, length).headroomLow
      ? ["kid_headroom_low"]
      : []
    : settings.warnings;
  const changed =
    settings.seriesLocked &&
    settings.kidLength !== null &&
    (length !== settings.kidLength || algorithm !== settings.kidAlgorithm);
  const preview = (() => {
    if (length === null && algorithm === null) return { tone: "dimmed", words: t("kidNone") };
    if (!agreed) return { tone: "yellow", words: t("kidPairIncomplete") };
    if (!kidFits(next, length).fits) return { tone: "red", words: t("kidDoesNotFit", { number: next, length }) };
    const kid = computeKid(next, length, algorithm);
    return kid ? { tone: "default", words: t("kidPreview", { kid, number: next }) } : undefined;
  })();
  return (
    <Card withBorder data-testid="kid-card">
      <Stack>
        <Title order={4}>{t("kid")}</Title>
        <Text size="sm" c="dimmed">
          {t("kidDescription")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <NumberInput
            label={t("kidLength")}
            description={t("kidLengthHint")}
            min={4}
            max={25}
            allowDecimal={false}
            allowNegative={false}
            error={errors.kidLength}
            value={length ?? ""}
            onChange={(v) => set("kidLength", v === "" ? null : Number(v))}
          />
          <Select
            label={t("kidAlgorithm")}
            description={t("kidAlgorithmHint")}
            clearable
            data={["mod10", "mod11"].map((value) => ({ value, label: t(`kidAlgorithm.${value}`) }))}
            error={errors.kidAlgorithm}
            value={algorithm}
            onChange={(v) => set("kidAlgorithm", v)}
          />
        </SimpleGrid>
        {preview &&
          (preview.tone === "red" || preview.tone === "yellow" ? (
            <Alert color={preview.tone} icon={<IconAlertCircle size={16} />} data-testid="kid-preview">
              {preview.words}
            </Alert>
          ) : (
            <Text size="sm" c={preview.tone === "dimmed" ? "dimmed" : undefined} data-testid="kid-preview">
              {preview.words}
            </Text>
          ))}
        {warnings.map((warning) => (
          <Alert key={warning} color="yellow" icon={<IconAlertCircle size={16} />} data-settings-warning={warning}>
            {`settingsWarning.${warning}` in invoicesCatalog.en
              ? t(`settingsWarning.${warning}`)
              : t("warningUnknown", { code: warning })}
          </Alert>
        ))}
        {changed && (
          <Alert color="yellow" icon={<IconInfoCircle size={16} />} role="note">
            {t("kidChangeWarning")}
          </Alert>
        )}
      </Stack>
    </Card>
  );
};

const personLabels = ["initials", "number", "name"] as const;
const workKinds = ["hours", "expenses", "milestones"] as const;

/**
 * The Work to invoice card (invoices work design D5, D6, D18): the VAT code
 * each kind of work's lines take in the wizard — every code, one no longer
 * offered named so, since a code kept as stored passes — and whether new
 * invoices carry a timesheet and how it names each person, with what that
 * tells the customer about the employees. Saved with the seller record; a
 * refused code or label is said on its own input.
 */
const WorkCard = ({
  stored,
  values,
  errors,
  set,
}: {
  /** The codes as saved: one kept as stored passes the server though it is no longer offered. */
  stored: InvoiceSettings["workVatCodes"];
  values: InvoiceSettings;
  errors: Record<string, string>;
  set: <K extends keyof InvoiceSettings>(key: K, value: InvoiceSettings[K], field?: string) => void;
}) => {
  const { t } = useInvoiceFormat();
  const codes = useQuery(vatCodesQueryOptions());
  // The codes offered for new lines, and any one stored that no longer is —
  // never another inactive code, which the server would refuse.
  const storedIds = new Set(Object.values(stored));
  const options = (codes.data ?? [])
    .filter((c) => c.active || storedIds.has(c.id))
    .map((c) => {
      const label = t("vatCodeOption", { code: c.code, name: c.name });
      return { value: String(c.id), label: c.active ? label : t("vatCodeNotOffered", { label }) };
    });
  return (
    <Card withBorder data-testid="work-card">
      <Stack>
        <Title order={4}>{t("workToInvoice")}</Title>
        <Text size="sm" c="dimmed">
          {t("workToInvoiceDescription")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          {workKinds.map((kind) => (
            <Select
              key={kind}
              label={t(`vatCodeFor.${kind}`)}
              data={options}
              allowDeselect={false}
              value={String(values.workVatCodes[kind])}
              error={errors[`workVatCodes.${kind}`]}
              onChange={(v) =>
                v !== null && set("workVatCodes", { ...values.workVatCodes, [kind]: Number(v) }, `workVatCodes.${kind}`)
              }
            />
          ))}
        </SimpleGrid>
        <Checkbox
          label={t("field.timesheetDefault")}
          checked={values.timesheetDefault}
          error={errors.timesheetDefault}
          onChange={(e) => set("timesheetDefault", e.currentTarget.checked)}
        />
        <Select
          label={t("field.timesheetPersonLabel")}
          description={t("timesheetPrivacyHint")}
          data={personLabels.map((value) => ({ value, label: t(`personLabel.${value}`) }))}
          allowDeselect={false}
          value={values.timesheetPersonLabel}
          error={errors.timesheetPersonLabel}
          onChange={(v) => v && set("timesheetPersonLabel", v as InvoiceSettings["timesheetPersonLabel"])}
        />
      </Stack>
    </Card>
  );
};

/** What Verify's answer looks like: the provider took the key, refused it, or could not be asked. */
const verifyColours: Record<string, string> = { ok: "green", unauthorized: "red", unreachable: "yellow" };

/**
 * The access point (EHF and KID design D7, D15), read from the server: the
 * form is drawn once the stored credentials are known, its legal entity
 * prefilled from them.
 */
const AccessPointSection = ({ meta }: { meta: InvoicesMeta }) => {
  const { t, date } = useInvoiceFormat();
  const stored = useQuery(accessPointQueryOptions());
  // A failed background refetch — after a save, say — keeps the form and the
  // key being typed in it; only a first read that failed says so instead.
  if (stored.isError && !stored.data) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadAccessPoint")}>
        {refusalMessage(stored.error, t, date)}
      </Alert>
    );
  }
  if (!stored.data) return <ContentSkeleton rows={3} rowHeight={36} />;
  return <AccessPointForm meta={meta} stored={stored.data} />;
};

/** A refusal said on the sub-card: what was attempted, and why it was refused. */
interface Refused {
  title: string;
  message: string;
}

/**
 * The access point's form: the provider — Storecove, the one there is — the
 * legal entity documents are sent as, and the API key, write-only: typed,
 * saved, and never shown again; a stored one is only said to be stored, and
 * an empty field keeps it. Verify asks the provider with the stored key and
 * says how it went; Remove asks first — the key cannot be shown again — and
 * is refused while a transmission is in flight, in the 409's words. Both wait
 * for a stored key. A key the provider refused is said with the day it was
 * refused. Every write invalidates `[INVOICES_QUERY_KEY]`, so the stored
 * credentials and meta are read again.
 */
const AccessPointForm = ({ meta, stored }: { meta: InvoicesMeta; stored: AccessPoint }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [legalEntityId, setLegalEntityId] = useState<number | string>(stored.legalEntityId ?? "");
  const [apiKey, setApiKey] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [verified, setVerified] = useState<string | null>(null);
  const [refused, setRefused] = useState<Refused | null>(null);
  const hasKey = stored.hasCredentials;
  const rejected = stored.rejectedAt !== undefined || (hasKey && meta.accessPointCredentialsRejected);
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const clear = () => {
    setVerified(null);
    setRefused(null);
  };
  // The access point's own ehf_unavailable (D7) is about its key — none
  // stored (Verify's 409) or a stored one that can no longer be opened (the
  // 503) — not about the installation's switch, which the module-wide words
  // describe.
  const accessPointRefusal = (error: unknown) =>
    refusalCode(error) === "ehf_unavailable" ? t("accessPointKeyUnreadable") : refusalMessage(error, t, date);
  const save = useMutation({
    mutationFn: () =>
      putAccessPoint({
        provider: "storecove",
        legalEntityId: Number(legalEntityId) || 0,
        // An empty field keeps the stored key: it is left out, never sent blank.
        ...(apiKey.trim() ? { apiKey } : {}),
      }),
    onMutate: clear,
    onSuccess: async () => {
      setApiKey("");
      setErrors({});
      await refresh();
      notifications.show({ color: "green", message: t("accessPointSaved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "legalEntityId" || field === "apiKey",
          "accessPoint",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) {
          notifications.show({ color: "red", title: t("couldNotSaveAccessPoint"), message: elsewhere.join(" ") });
        }
        return;
      }
      setRefused({ title: t("couldNotSaveAccessPoint"), message: accessPointRefusal(error) });
    },
  });
  const verify = useMutation({
    mutationFn: verifyAccessPoint,
    onMutate: clear,
    onSuccess: async ({ result }) => {
      setVerified(result);
      await refresh();
    },
    onError: (error) => setRefused({ title: t("couldNotVerifyAccessPoint"), message: accessPointRefusal(error) }),
  });
  const remove = useMutation({
    mutationFn: deleteAccessPoint,
    onMutate: clear,
    onSuccess: async () => {
      setApiKey("");
      await refresh();
      notifications.show({ color: "green", message: t("accessPointRemoved") });
    },
    onError: (error) => setRefused({ title: t("couldNotRemoveAccessPoint"), message: refusalMessage(error, t, date) }),
  });
  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("removeAccessPointTitle"),
      children: <Text size="sm">{t("removeAccessPointConfirm")}</Text>,
      labels: { confirm: t("removeAccessPoint"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });
  const busy = save.isPending || verify.isPending || remove.isPending;
  return (
    <Card withBorder data-testid="access-point">
      <Stack>
        <Group justify="space-between">
          <Title order={5}>{t("accessPoint")}</Title>
          {hasKey && (
            <Badge color="green" variant="light">
              {t("apiKeyStoredBadge")}
            </Badge>
          )}
        </Group>
        <Text size="sm" c="dimmed">
          {t("accessPointHint")}
        </Text>
        {rejected && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("accessPointRejectedTitle")}>
            {stored.rejectedAt
              ? t("accessPointRejectedOn", { at: dateTime(stored.rejectedAt) })
              : t("accessPointRejected")}
          </Alert>
        )}
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <TextInput label={t("accessPointProvider")} readOnly value={t("provider.storecove")} />
          <NumberInput
            label={t("legalEntityId")}
            description={t("legalEntityIdHint")}
            min={1}
            allowDecimal={false}
            allowNegative={false}
            error={errors.legalEntityId}
            value={legalEntityId}
            onChange={(v) => {
              setLegalEntityId(v);
              setErrors((current) => {
                const next = { ...current };
                delete next.legalEntityId;
                return next;
              });
            }}
          />
          <PasswordInput
            label={t("apiKey")}
            description={hasKey ? t("apiKeyStoredHint") : t("apiKeyNewHint")}
            autoComplete="new-password"
            error={errors.apiKey}
            value={apiKey}
            onChange={(e) => {
              setApiKey(e.currentTarget.value);
              setErrors((current) => {
                const next = { ...current };
                delete next.apiKey;
                return next;
              });
            }}
          />
        </SimpleGrid>
        {verified && (
          <Alert color={verifyColours[verified] ?? "gray"} data-verify-result={verified}>
            {`verify.${verified}` in invoicesCatalog.en
              ? t(`verify.${verified}`)
              : t("warningUnknown", { code: verified })}
          </Alert>
        )}
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={refused.title}>
            {refused.message}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button
            variant="default"
            color="red"
            leftSection={<IconTrash size={16} />}
            disabled={busy || !hasKey}
            loading={remove.isPending}
            onClick={confirmRemove}
          >
            {t("removeAccessPoint")}
          </Button>
          <Button
            variant="default"
            disabled={busy || !hasKey}
            loading={verify.isPending}
            onClick={() => verify.mutate()}
          >
            {t("verifyAccessPoint")}
          </Button>
          <Button disabled={busy} loading={save.isPending} onClick={() => save.mutate()}>
            {t("saveAccessPoint")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};

type CodeModal = { mode: "create" } | { mode: "edit"; code: VatCode } | { mode: "rates"; code: VatCode } | null;

const VatCodesSection = () => {
  const { t, percent, date } = useInvoiceFormat();
  const codes = useQuery(vatCodesQueryOptions());
  const meta = useQuery(invoicesMetaQueryOptions());
  const [modal, setModal] = useState<CodeModal>(null);
  const today = meta.data?.today ?? "";
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("vatCodes")}</Title>
          <Button leftSection={<IconPlus size={16} />} variant="light" onClick={() => setModal({ mode: "create" })}>
            {t("addVatCode")}
          </Button>
        </Group>
        {codes.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadVatCodes")}>
            {refusalMessage(codes.error, t, date)}
          </Alert>
        )}
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
                const rate = rateOn(code, today);
                return (
                  <Table.Tr key={code.id}>
                    <Table.Td>{code.code}</Table.Td>
                    <Table.Td>
                      {code.name} {!code.active && <Badge color="gray">{t("inactive")}</Badge>}
                    </Table.Td>
                    <Table.Td>{code.ehfCategory}</Table.Td>
                    <Table.Td>{code.safTCode}</Table.Td>
                    <Table.Td ta="right">{rate === undefined ? t("notAvailable") : percent(rate)}</Table.Td>
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
      {modal?.mode === "edit" && (
        <VatCodeForm
          key={modal.code.revision}
          code={modal.code}
          latestRevision={codes.data?.find((c) => c.id === modal.code.id)?.revision}
          today={today}
          onReloaded={(latest) => setModal({ mode: "edit", code: latest })}
          onClose={() => setModal(null)}
        />
      )}
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
interface VatCodeFormProps {
  /** The code being edited; none for a new one. */
  code?: VatCode;
  /** The revision the codes list last answered for it: newer when someone else saved meanwhile. */
  latestRevision?: number;
  today: string;
  /** Called with the latest version of the code after Reload; the form remounts on it. */
  onReloaded?: (latest: VatCode) => void;
  onClose: () => void;
}

/**
 * A VAT code's own fields. An edit refused as stale, or a newer revision seen
 * while the form is open, says the code changed and offers Reload, as the
 * seller form does — never the server's English revision sentence.
 */
const VatCodeForm = ({ code, latestRevision, today, onReloaded, onClose }: VatCodeFormProps) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState(false);
  const [reloadFailed, setReloadFailed] = useState(false);
  const stale = Boolean(code) && (conflict || (latestRevision ?? 0) > (code?.revision ?? 0));
  const reload = useMutation({
    mutationFn: async () => {
      const codes = await queryClient.fetchQuery({ ...vatCodesQueryOptions(), staleTime: 0 });
      const latest = codes.find((c) => c.id === code?.id);
      if (!latest) throw new Error("gone");
      return latest;
    },
    onMutate: () => setReloadFailed(false),
    onSuccess: (latest) => onReloaded?.(latest),
    onError: () => setReloadFailed(true),
  });
  const [values, setValues] = useState({
    code: code?.code ?? "",
    name: code?.name ?? "",
    safTCode: code?.safTCode ?? "",
    ehfCategory: code?.ehfCategory ?? "S",
    exemptionReason: code?.exemptionReason ?? "",
    active: code?.active ?? true,
    ratePercent: defaultRate(code?.ehfCategory ?? "S") as number | string,
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
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSaveVatCode"), message: refusalMessage(error, t, date) });
    },
  });
  return (
    <Modal opened onClose={onClose} title={code ? t("editVatCode", { code: code.code }) : t("addVatCode")}>
      <Stack>
        {stale && (
          <StaleAlert
            title={t("vatCodeChangedTitle")}
            message={t("vatCodeChangedMessage")}
            reloadFailedMessage={reloadFailed ? t("couldNotReloadVatCode") : undefined}
            reloading={reload.isPending}
            onReload={() => reload.mutate()}
          />
        )}
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
          onChange={(v) => {
            const category = v ?? "S";
            // A new code's first rate follows the category, as a new period's
            // does: only S is taxed at a rate above 0 %, and the server
            // refuses any other category a rate that is not.
            setValues({ ...values, ehfCategory: category, ...(code ? {} : { ratePercent: defaultRate(category) }) });
          }}
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
  const { t, percent } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [ratePercent, setRatePercent] = useState<number | string>(defaultRate(code.ehfCategory));
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
                <Table.Td>{percent(r.ratePercent)}</Table.Td>
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

import {
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Checkbox,
  ColorInput,
  FileButton,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  appUrl,
  type BrandTokens,
  BUILTIN_PRIMARY,
  LOGO_SLOTS,
  PageSection,
  tryComputeTokens,
  useI18n,
} from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import {
  type Brand,
  type BrandCampaign,
  type BrandCampaignInputs,
  type BrandSummary,
  brandCampaignsQueryKey,
  brandQueryKey,
  campaignLogoPath,
  createBrandCampaign,
  deleteBrandCampaign,
  fetchBrand,
  type LogoSlot,
  listBrandCampaigns,
  removeCampaignLogo,
  saveBrandCampaign,
  uploadCampaignLogo,
} from "../../api/brands";
import "../../i18n";
import { BrandPreview } from "./-brand-preview";

const HEX = /^#[0-9a-f]{6}$/i;
const DAY_MS = 24 * 60 * 60 * 1000;

type ErrorBody = { status?: number; code?: string; fields?: Record<string, string[]>; message?: string };

const BADGE_COLOUR = { active: "green", upcoming: "blue", ended: "gray", disabled: "yellow" } as const;

/** A `YYYY-MM-DD` date as a UTC instant, for counting days and formatting without the browser's zone. */
const utc = (iso: string) => new Date(`${iso}T00:00:00Z`);

/** The days a range spans, both ends counted. */
const daysBetween = (from: string, to: string) => Math.round((utc(to).getTime() - utc(from).getTime()) / DAY_MS) + 1;

interface Values {
  name: string;
  startsOn: string | null;
  endsOn: string | null;
  yearly: boolean;
  enabled: boolean;
  affectsEmail: boolean;
  changeColour: boolean;
  primary: string;
  title: string;
}

const valuesOf = (c: BrandCampaign | undefined, brandPrimary: string): Values => ({
  name: c?.name ?? "",
  startsOn: c?.startsOn ?? null,
  endsOn: c?.endsOn ?? null,
  yearly: c?.yearly ?? false,
  enabled: c?.enabled ?? true,
  affectsEmail: c?.affectsEmail ?? false,
  changeColour: Boolean(c?.overlay.primary),
  primary: c?.overlay.primary ?? brandPrimary,
  title: c?.overlay.title ?? "",
});

/** The logos staged in the form: a file to upload, or the mark that the slot is to be cleared. */
type Staged = Partial<Record<LogoSlot, File | "remove">>;

/** The campaigns of the brands (brands design B22-B24): a list with where each stands, and the editor of one. */
export const BrandCampaigns = ({ brands }: { brands: BrandSummary[] }) => {
  const { t, locale } = useI18n("host");
  const qc = useQueryClient();
  const usable = brands.filter((b) => b.state !== "archived");
  const [chosen, setChosen] = useState<string | undefined>();
  const [editing, setEditing] = useState<BrandCampaign | "new" | null>(null);
  const brandId = usable.find((b) => b.id === chosen)?.id ?? usable[0]?.id;

  const list = useQuery({
    queryKey: brandCampaignsQueryKey(brandId ?? ""),
    queryFn: () => listBrandCampaigns(brandId as string),
    enabled: Boolean(brandId),
  });
  const brand = useQuery({
    queryKey: brandQueryKey(brandId ?? ""),
    queryFn: () => fetchBrand(brandId as string),
    enabled: Boolean(brandId),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteBrandCampaign(brandId as string, id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: brandCampaignsQueryKey(brandId as string) });
      notifications.show({ color: "green", message: t("campaigns.deleted") });
    },
    onError: () => notifications.show({ color: "red", message: t("campaigns.error.generic") }),
  });

  const day = (iso: string, withYear: boolean) =>
    new Intl.DateTimeFormat(locale, {
      day: "numeric",
      month: "short",
      ...(withYear ? { year: "numeric" } : {}),
      timeZone: "UTC",
    }).format(utc(iso));
  const range = (c: BrandCampaign) =>
    c.yearly
      ? t("campaigns.rangeYearly", { from: day(c.startsOn, false), to: day(c.endsOn, false) })
      : t("campaigns.range", { from: day(c.startsOn, true), to: day(c.endsOn, true) });

  const confirmDelete = (c: BrandCampaign) =>
    modals.openConfirmModal({
      title: t("campaigns.deleteTitle", { name: c.name }),
      children: <Text size="sm">{t("campaigns.deleteBody")}</Text>,
      labels: { confirm: t("campaigns.delete"), cancel: t("campaigns.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(c.id),
    });

  if (usable.length === 0) return <Text c="dimmed">{t("campaigns.noBrands")}</Text>;

  return (
    <Stack gap="lg">
      <PageSection title={t("campaigns.title")}>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("campaigns.intro")}
          </Text>
          <Select
            label={t("campaigns.brand")}
            data={usable.map((b) => ({ value: b.id, label: b.name }))}
            value={brandId ?? null}
            onChange={(v) => {
              setChosen(v ?? undefined);
              setEditing(null);
            }}
            allowDeselect={false}
            maw={360}
          />
          {list.isError && <Alert color="red">{t("campaigns.loadFailed")}</Alert>}
          {list.data && list.data.items.length === 0 && <Text c="dimmed">{t("campaigns.none")}</Text>}
          {list.data && list.data.items.length > 0 && (
            <Table data-testid="campaign-list" verticalSpacing="xs">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("campaigns.name")}</Table.Th>
                  <Table.Th>{t("campaigns.dates")}</Table.Th>
                  <Table.Th>{t("campaigns.status")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.data.items.map((c) => (
                  <Table.Tr key={c.id} data-testid={`campaign-${c.id}`}>
                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        {c.overlay.primary && (
                          <Box
                            w={14}
                            h={14}
                            style={{ background: c.overlay.primary, borderRadius: 3, flex: "none" }}
                            aria-hidden
                          />
                        )}
                        <Text fw={500}>{c.name}</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td>{range(c)}</Table.Td>
                    <Table.Td>
                      <Badge color={BADGE_COLOUR[c.status]} variant="light">
                        {t(`campaigns.status.${c.status}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs" justify="flex-end" wrap="nowrap">
                        <Button size="compact-sm" variant="default" onClick={() => setEditing(c)}>
                          {t("campaigns.edit")}
                        </Button>
                        <Button size="compact-sm" variant="subtle" color="red" onClick={() => confirmDelete(c)}>
                          {t("campaigns.delete")}
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
          {editing === null && (
            <Group>
              <Button onClick={() => setEditing("new")} disabled={!brand.data}>
                {t("campaigns.add")}
              </Button>
            </Group>
          )}
        </Stack>
      </PageSection>

      {editing !== null && brand.data && brandId && (
        <CampaignEditor
          key={editing === "new" ? "new" : `${editing.id}-${editing.revision}`}
          brand={brand.data}
          campaign={editing === "new" ? undefined : editing}
          onDone={() => setEditing(null)}
        />
      )}
    </Stack>
  );
};

const CampaignEditor = ({
  brand,
  campaign,
  onDone,
}: {
  brand: Brand;
  campaign: BrandCampaign | undefined;
  onDone: () => void;
}) => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const brandPrimary = brand.published?.primary ?? BUILTIN_PRIMARY;
  const [values, setValues] = useState<Values>(() => valuesOf(campaign, brandPrimary));
  const [staged, setStaged] = useState<Staged>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const set = <K extends keyof Values>(key: K, value: Values[K]) => setValues((prev) => ({ ...prev, [key]: value }));

  // The overlay's scale is computed here with the algorithm the server uses, for the preview only; the
  // server recomputes it on every save and refuses a colour that does not read (B38).
  const primaryOk = HEX.test(values.primary);
  const overlayTokens = useMemo(
    () => (values.changeColour && primaryOk ? tryComputeTokens(values.primary.toLowerCase(), "md") : null),
    [values.changeColour, primaryOk, values.primary],
  );
  const tokens = overlayTokens ?? (brand.published?.tokens as unknown as BrandTokens | undefined) ?? null;
  const primaryError = !values.changeColour
    ? undefined
    : !primaryOk
      ? t("brands.primaryInvalid")
      : !overlayTokens
        ? t("brands.primaryRange")
        : fieldErrors.primary;

  const days = values.startsOn && values.endsOn ? daysBetween(values.startsOn, values.endsOn) : null;
  const orderError =
    days !== null && days < 1
      ? t("campaigns.error.order")
      : values.yearly && days !== null && days > 365
        ? t("campaigns.error.tooLong")
        : undefined;
  const ready =
    values.name.trim() !== "" &&
    values.startsOn !== null &&
    values.endsOn !== null &&
    !orderError &&
    (!values.changeColour || (primaryOk && overlayTokens !== null));

  const logoUrl = (slot: LogoSlot): string | undefined => {
    const stage = staged[slot];
    if (stage === "remove") return undefined;
    if (stage instanceof File)
      return typeof URL.createObjectURL === "function" ? URL.createObjectURL(stage) : undefined;
    const own = campaign?.overlay.logos[slot];
    if (own) return appUrl(campaignLogoPath(brand.id, (campaign as BrandCampaign).id, slot, own.sha256));
    const inherited = brand.published?.logos[slot];
    return inherited
      ? appUrl(`/api/v1/identity/brands/${brand.id}/logos/${slot}?v=${inherited.sha256.slice(0, 12)}`)
      : undefined;
  };

  const explain = (error: unknown): string => {
    const e = error as ErrorBody;
    switch (e.code) {
      case "campaign_overlap":
        return e.message || t("campaigns.error.overlap");
      case "campaign_too_long":
        return t("campaigns.error.tooLong");
      case "brand_primary_range":
        return t("brands.primaryRange");
      case "brand_contrast":
        return t("brands.error.contrast");
      case "brand_archived":
        return t("campaigns.error.archived");
      default:
        return e.message || t("campaigns.error.generic");
    }
  };

  const save = useMutation({
    mutationFn: async () => {
      const body: BrandCampaignInputs = {
        name: values.name.trim(),
        startsOn: values.startsOn as string,
        endsOn: values.endsOn as string,
        yearly: values.yearly,
        enabled: values.enabled,
        affectsEmail: values.affectsEmail,
        overlay: {
          ...(values.changeColour ? { primary: values.primary.toLowerCase() } : {}),
          ...(values.title.trim() ? { title: values.title.trim() } : {}),
        },
      };
      const saved = campaign
        ? await saveBrandCampaign(brand.id, campaign.id, body)
        : await createBrandCampaign(brand.id, body);
      for (const slot of LOGO_SLOTS) {
        const stage = staged[slot];
        if (stage === "remove") await removeCampaignLogo(brand.id, saved.id, slot);
        else if (stage) await uploadCampaignLogo(brand.id, saved.id, slot, stage);
      }
      return saved;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: brandCampaignsQueryKey(brand.id) });
      notifications.show({ color: "green", message: t("campaigns.saved") });
      onDone();
    },
    onError: (error) => {
      const e = error as ErrorBody;
      const next: Record<string, string> = {};
      for (const [field, messages] of Object.entries(e.fields ?? {})) next[field] = messages[0] ?? "";
      setFieldErrors(next);
      notifications.show({ color: "red", message: explain(error) });
      void qc.invalidateQueries({ queryKey: brandCampaignsQueryKey(brand.id) });
    },
  });

  const storage = brand.logoStorageAvailable;

  return (
    <PageSection title={campaign ? t("campaigns.editTitle", { name: campaign.name }) : t("campaigns.newTitle")}>
      <Stack gap="lg" data-testid="campaign-editor">
        <Alert color="blue" variant="light">
          {t("campaigns.liveOnSave")}
        </Alert>

        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("campaigns.name")}
            description={t("campaigns.nameHelp")}
            value={values.name}
            maxLength={100}
            onChange={(e) => set("name", e.currentTarget.value)}
            error={fieldErrors.name}
          />
          <Stack gap="xs" justify="flex-end">
            <Switch
              label={t("campaigns.enabled")}
              description={t("campaigns.enabledHelp")}
              checked={values.enabled}
              onChange={(e) => set("enabled", e.currentTarget.checked)}
            />
          </Stack>
          <DatePickerInput
            label={t("campaigns.startsOn")}
            description={values.yearly ? t("campaigns.startsOnYearlyHelp") : undefined}
            valueFormat="YYYY-MM-DD"
            value={values.startsOn}
            onChange={(v) => set("startsOn", v)}
            error={fieldErrors.startsOn}
          />
          <DatePickerInput
            label={t("campaigns.endsOn")}
            valueFormat="YYYY-MM-DD"
            value={values.endsOn}
            minDate={values.startsOn ?? undefined}
            onChange={(v) => set("endsOn", v)}
            error={orderError ?? fieldErrors.endsOn}
          />
        </SimpleGrid>
        <Switch
          label={t("campaigns.yearly")}
          description={t("campaigns.yearlyHelp")}
          checked={values.yearly}
          onChange={(e) => set("yearly", e.currentTarget.checked)}
        />

        <Stack gap="sm">
          <Text fw={600}>{t("campaigns.overlay")}</Text>
          <Text size="sm" c="dimmed">
            {t("campaigns.overlayHelp")}
          </Text>
          <Switch
            label={t("campaigns.changeColour")}
            checked={values.changeColour}
            onChange={(e) => set("changeColour", e.currentTarget.checked)}
          />
          {values.changeColour && (
            <ColorInput
              label={t("brands.primary")}
              description={t("brands.primaryHelp")}
              format="hex"
              value={values.primary}
              onChange={(v) => set("primary", v)}
              error={primaryError}
              withEyeDropper={false}
              maw={360}
            />
          )}
          <TextInput
            label={t("campaigns.titleLabel")}
            description={t("campaigns.titleHelp")}
            value={values.title}
            maxLength={60}
            onChange={(e) => set("title", e.currentTarget.value)}
            error={fieldErrors.title}
            maw={360}
          />
        </Stack>

        <Stack gap="sm">
          <Text fw={600}>{t("campaigns.logos")}</Text>
          {!storage && <Alert color="gray">{t("brands.noStorage")}</Alert>}
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            {LOGO_SLOTS.map((slot) => {
              const src = logoUrl(slot);
              const own = staged[slot] instanceof File || (staged[slot] !== "remove" && campaign?.overlay.logos[slot]);
              return (
                <Card key={slot} withBorder padding="sm" data-testid={`campaign-logo-${slot}`}>
                  <Stack gap="xs">
                    <Text fw={500}>{t(`brands.logo.${slot}`)}</Text>
                    <Box
                      h={56}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        background: slot === "mark_dark" ? "#0d0f14" : "#f7f8fc",
                        borderRadius: 6,
                      }}
                    >
                      {src ? (
                        <img src={src} alt={t(`brands.logo.${slot}`)} style={{ maxHeight: 44, maxWidth: "90%" }} />
                      ) : (
                        <Text size="xs" c="dimmed">
                          {t("brands.logoNone")}
                        </Text>
                      )}
                    </Box>
                    <Text size="xs" c="dimmed">
                      {own ? t("campaigns.logoOwn") : t("campaigns.logoInherited")}
                    </Text>
                    <Group gap="xs">
                      <FileButton
                        accept="image/png,image/jpeg"
                        onChange={(file) => file && setStaged((prev) => ({ ...prev, [slot]: file }))}
                      >
                        {(props) => (
                          <Button disabled={!storage} {...props} size="compact-sm" variant="default">
                            {own ? t("brands.logoReplace") : t("brands.logoUpload")}
                          </Button>
                        )}
                      </FileButton>
                      {own && (
                        <Button
                          size="compact-sm"
                          variant="subtle"
                          color="red"
                          onClick={() =>
                            setStaged((prev) => {
                              const next = { ...prev };
                              if (campaign?.overlay.logos[slot]) next[slot] = "remove";
                              else delete next[slot];
                              return next;
                            })
                          }
                        >
                          {t("brands.logoRemove")}
                        </Button>
                      )}
                    </Group>
                  </Stack>
                </Card>
              );
            })}
          </SimpleGrid>
        </Stack>

        <Checkbox
          label={t("campaigns.affectsEmail")}
          description={t("campaigns.affectsEmailHelp")}
          checked={values.affectsEmail}
          onChange={(e) => set("affectsEmail", e.currentTarget.checked)}
        />

        <Stack gap="xs">
          <Text fw={600}>{t("brands.section.preview")}</Text>
          <BrandPreview tokens={tokens} title={values.title.trim() || brand.published?.title || ""} logoFor={logoUrl} />
        </Stack>

        <Group justify="flex-end">
          <Button variant="default" onClick={onDone} disabled={save.isPending}>
            {t("campaigns.cancel")}
          </Button>
          <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!ready}>
            {t("campaigns.save")}
          </Button>
        </Group>
      </Stack>
    </PageSection>
  );
};

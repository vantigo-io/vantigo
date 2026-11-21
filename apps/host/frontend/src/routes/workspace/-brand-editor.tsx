import {
  Alert,
  Badge,
  Box,
  Button,
  Card,
  ColorInput,
  FileButton,
  Group,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  appUrl,
  BRAND_RADII,
  type BrandRadius,
  BUILTIN_PRIMARY,
  contrastChecks,
  LOGO_SLOTS,
  PageSection,
  scaleOf,
  setBrandPreview,
  suggestPrimary,
  tryComputeTokens,
  useI18n,
} from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import {
  type Brand,
  type BrandInputs,
  brandQueryKey,
  brandsQueryKey,
  discardBrandDraft,
  draftLogoPath,
  type LogoSlot,
  publishBrand,
  removeBrandLogo,
  saveBrandDraft,
  uploadBrandLogo,
} from "../../api/brands";
import "../../i18n";
import { BrandPreview } from "./-brand-preview";

const HEX = /^#[0-9a-f]{6}$/i;

interface Values {
  name: string;
  title: string;
  primary: string;
  radius: BrandRadius;
  email: string;
  phone: string;
  url: string;
}

/** The version being edited: the draft, else the published one. */
const editingVersion = (brand: Brand) => brand.draft ?? brand.published;

const valuesOf = (brand: Brand): Values => {
  const v = brand.published;
  const d = brand.draft;
  const src = d ?? v;
  return {
    name: src?.name ?? "",
    title: src?.title ?? "",
    primary: src?.primary ?? BUILTIN_PRIMARY,
    radius: (src?.radius ?? "md") as BrandRadius,
    email: (d ?? v)?.support.email ?? "",
    phone: (d ?? v)?.support.phone ?? "",
    url: (d ?? v)?.support.url ?? "",
  };
};

const inputsOf = (v: Values): BrandInputs => ({
  name: v.name,
  title: v.title,
  primary: v.primary,
  radius: v.radius,
  support: { email: v.email || undefined, phone: v.phone || undefined, url: v.url || undefined },
});

type ErrorBody = { status?: number; code?: string; fields?: Record<string, string[]>; message?: string };

export const BrandEditor = ({ brand }: { brand: Brand }) => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const initial = useMemo(() => valuesOf(brand), [brand]);
  const [values, setValues] = useState<Values>(initial);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [logoError, setLogoError] = useState<Partial<Record<LogoSlot, string>>>({});

  const set = <K extends keyof Values>(key: K, value: Values[K]) => setValues((prev) => ({ ...prev, [key]: value }));
  const dirty = JSON.stringify(values) !== JSON.stringify(initial);

  // The scale, the contrast results and the preview are computed here with the same algorithm as the
  // server's (a port, tested against one golden file); the server recomputes them on every save.
  const primaryOk = HEX.test(values.primary);
  const tokens = useMemo(
    () => (primaryOk ? tryComputeTokens(values.primary.toLowerCase(), values.radius) : null),
    [primaryOk, values.primary, values.radius],
  );
  const checks = useMemo(
    () => (tokens ? contrastChecks(scaleOf(values.primary.toLowerCase())) : []),
    [tokens, values.primary],
  );
  const failing = checks.filter((c) => !c.pass);
  const suggestion = useMemo(
    () => (failing.length > 0 ? suggestPrimary(values.primary.toLowerCase()) : null),
    [failing.length, values.primary],
  );
  const primaryError = !primaryOk
    ? t("brands.primaryInvalid")
    : !tokens
      ? t("brands.primaryRange")
      : (fieldErrors.primary ?? undefined);

  const logos = editingVersion(brand)?.logos ?? {};
  const logoUrl = (slot: LogoSlot): string | undefined => {
    const logo = logos[slot];
    return logo ? appUrl(draftLogoPath(brand.id, slot, logo.sha256)) : undefined;
  };

  const refresh = (next: Brand) => {
    qc.setQueryData(brandQueryKey(brand.id), next);
    void qc.invalidateQueries({ queryKey: brandsQueryKey });
  };

  const explain = (error: unknown): string => {
    const e = error as ErrorBody;
    if (e.status === 409 && e.code === "stale_revision") return t("brands.error.stale");
    if (e.status === 409 && e.code === "no_draft") return t("brands.error.noDraft");
    if (e.code === "brand_contrast") return t("brands.error.contrast");
    return e.message || t("brands.error.generic");
  };

  const onFailure = (error: unknown) => {
    const e = error as ErrorBody;
    const next: Record<string, string> = {};
    for (const [field, messages] of Object.entries(e.fields ?? {})) next[field] = messages[0] ?? "";
    setFieldErrors(next);
    notifications.show({ color: "red", message: explain(error) });
  };

  const save = useMutation({
    mutationFn: () => saveBrandDraft(brand.id, brand.revision, inputsOf(values)),
    onSuccess: (next) => {
      setFieldErrors({});
      refresh(next);
      notifications.show({ color: "green", message: t("brands.saved") });
    },
    onError: onFailure,
  });

  const publish = useMutation({
    mutationFn: async () => {
      const current = dirty ? await saveBrandDraft(brand.id, brand.revision, inputsOf(values)) : brand;
      return publishBrand(brand.id, current.revision);
    },
    onSuccess: (next) => {
      setFieldErrors({});
      refresh(next);
      notifications.show({ color: "green", message: t("brands.publishedDone") });
    },
    onError: onFailure,
  });

  const discard = useMutation({
    mutationFn: () => discardBrandDraft(brand.id, brand.revision),
    onSuccess: (next) => {
      setFieldErrors({});
      setValues(valuesOf(next));
      refresh(next);
      notifications.show({ color: "green", message: t("brands.discarded") });
    },
    onError: onFailure,
  });

  const previewAsMe = useMutation({
    mutationFn: async () => (dirty ? saveBrandDraft(brand.id, brand.revision, inputsOf(values)) : brand),
    onSuccess: (next) => {
      refresh(next);
      setBrandPreview(brand.id);
      notifications.show({ color: "blue", message: t("brands.previewStarted") });
    },
    onError: onFailure,
  });

  const upload = useMutation({
    mutationFn: ({ slot, file }: { slot: LogoSlot; file: File }) => uploadBrandLogo(brand.id, slot, file),
    onSuccess: (next, { slot }) => {
      setLogoError((prev) => ({ ...prev, [slot]: undefined }));
      refresh(next);
    },
    onError: (error, { slot }) => {
      const code = (error as ErrorBody).code ?? "other";
      const key = `brands.logoError.${code}`;
      const text = t(key);
      setLogoError((prev) => ({ ...prev, [slot]: text === key ? t("brands.logoError.other") : text }));
    },
  });

  const remove = useMutation({
    mutationFn: (slot: LogoSlot) => removeBrandLogo(brand.id, slot),
    onSuccess: (next) => refresh(next),
    onError: onFailure,
  });

  const busy = save.isPending || publish.isPending || discard.isPending || previewAsMe.isPending;
  const hasDraft = brand.draft !== null;

  return (
    <Stack gap="lg">
      <Group justify="space-between">
        <Group gap="xs">
          <Badge color={hasDraft || dirty ? "yellow" : "green"} variant="light">
            {hasDraft || dirty ? t("brands.unpublishedChanges") : t("brands.published")}
          </Badge>
          <Text size="sm" c="dimmed">
            {t("brands.version", { version: brand.publishedVersion })}
          </Text>
        </Group>
        <Group gap="xs">
          <Button
            variant="default"
            onClick={() => previewAsMe.mutate()}
            loading={previewAsMe.isPending}
            disabled={busy || !tokens}
          >
            {t("brands.previewAsMe")}
          </Button>
          <Button
            variant="default"
            onClick={() => discard.mutate()}
            loading={discard.isPending}
            disabled={busy || !hasDraft}
          >
            {t("brands.discardDraft")}
          </Button>
          <Button
            variant="light"
            onClick={() => save.mutate()}
            loading={save.isPending}
            disabled={busy || !dirty || !tokens}
          >
            {t("brands.saveDraft")}
          </Button>
          <Button
            onClick={() => publish.mutate()}
            loading={publish.isPending}
            disabled={busy || !tokens || (!dirty && !hasDraft) || failing.length > 0}
          >
            {t("brands.publish")}
          </Button>
        </Group>
      </Group>

      {brand.importNotice && (
        <Alert color="yellow" title={t("brands.importNoticeTitle")}>
          {brand.importNotice}
        </Alert>
      )}

      <PageSection title={t("brands.section.identity")}>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("brands.name")}
            description={t("brands.nameHelp")}
            value={values.name}
            maxLength={100}
            onChange={(e) => set("name", e.currentTarget.value)}
            error={fieldErrors.name}
          />
          <TextInput
            label={t("brands.titleLabel")}
            description={t("brands.titleHelp")}
            value={values.title}
            maxLength={60}
            onChange={(e) => set("title", e.currentTarget.value)}
            error={fieldErrors.title}
          />
        </SimpleGrid>
      </PageSection>

      <PageSection title={t("brands.section.colour")}>
        <Stack gap="md">
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <ColorInput
              label={t("brands.primary")}
              description={t("brands.primaryHelp")}
              format="hex"
              value={values.primary}
              onChange={(v) => set("primary", v)}
              error={primaryError}
              withEyeDropper={false}
            />
            <Select
              label={t("brands.radius")}
              data={BRAND_RADII.map((r) => ({ value: r, label: t(`brands.radius.${r}`) }))}
              value={values.radius}
              onChange={(v) => v && set("radius", v as BrandRadius)}
              allowDeselect={false}
            />
          </SimpleGrid>
          {tokens && (
            <Box>
              <Text size="sm" fw={500} mb={4}>
                {t("brands.scale")}
              </Text>
              <Group gap={4} wrap="nowrap" data-testid="brand-scale">
                {tokens.light.shades.map((shade, i) => (
                  <Box
                    key={shade + String(i)}
                    title={t("brands.shade", { n: i })}
                    data-shade={shade}
                    style={{
                      flex: 1,
                      height: 36,
                      background: shade,
                      borderRadius: 4,
                      border: "1px solid var(--mantine-color-default-border)",
                    }}
                  />
                ))}
              </Group>
            </Box>
          )}
          {tokens && (
            <Box>
              <Title order={5}>{t("brands.contrast")}</Title>
              <Text size="sm" c="dimmed" mb="xs">
                {t("brands.contrastHelp")}
              </Text>
              <Table withTableBorder data-testid="brand-contrast">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("brands.pair")}</Table.Th>
                    <Table.Th>{t("brands.scheme.light")}</Table.Th>
                    <Table.Th>{t("brands.scheme.dark")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {(["filled", "filledHover", "linkPage", "linkCard", "linkTint"] as const).map((pair) => (
                    <Table.Tr key={pair}>
                      <Table.Td>{t(`brands.pair.${pair}`)}</Table.Td>
                      {(["light", "dark"] as const).map((scheme) => {
                        const c = checks.find((x) => x.pair === pair && x.scheme === scheme);
                        return (
                          <Table.Td key={scheme} data-pass={c?.pass ? "true" : "false"}>
                            <Group gap={6}>
                              <Text size="sm">{c ? `${c.ratio.toFixed(2)} : 1` : "—"}</Text>
                              <Badge size="sm" color={c?.pass ? "green" : "red"} variant="light">
                                {c?.pass ? t("brands.pass") : t("brands.fail")}
                              </Badge>
                            </Group>
                          </Table.Td>
                        );
                      })}
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
              {failing.length > 0 && (
                <Alert color="yellow" mt="sm">
                  <Stack gap="xs">
                    <Text size="sm">{t("brands.contrastFailing")}</Text>
                    {suggestion && (
                      <Group gap="xs">
                        <Button size="compact-sm" variant="light" onClick={() => set("primary", suggestion)}>
                          {t("brands.useSuggestion", { colour: suggestion })}
                        </Button>
                        <Text size="xs" c="dimmed">
                          {t("brands.suggestionHelp")}
                        </Text>
                      </Group>
                    )}
                  </Stack>
                </Alert>
              )}
            </Box>
          )}
        </Stack>
      </PageSection>

      <PageSection title={t("brands.section.logos")}>
        <Stack gap="sm">
          {!brand.logoStorageAvailable && <Alert color="gray">{t("brands.noStorage")}</Alert>}
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            {LOGO_SLOTS.map((slot) => {
              const src = logoUrl(slot);
              return (
                <Card key={slot} withBorder padding="sm" data-testid={`logo-${slot}`}>
                  <Stack gap="xs">
                    <Text fw={500}>{t(`brands.logo.${slot}`)}</Text>
                    <Text size="xs" c="dimmed">
                      {t(`brands.logoHelp.${slot}`)}
                    </Text>
                    <Box
                      h={64}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        background: slot === "mark_dark" ? "#0d0f14" : "#f7f8fc",
                        borderRadius: 6,
                      }}
                    >
                      {src ? (
                        <img src={src} alt={t(`brands.logo.${slot}`)} style={{ maxHeight: 48, maxWidth: "90%" }} />
                      ) : (
                        <Text size="xs" c="dimmed">
                          {t("brands.logoNone")}
                        </Text>
                      )}
                    </Box>
                    <Group gap="xs">
                      <FileButton
                        accept="image/png,image/jpeg"
                        onChange={(file) => file && upload.mutate({ slot, file })}
                      >
                        {(props) => (
                          <Button
                            disabled={!brand.logoStorageAvailable}
                            {...props}
                            size="compact-sm"
                            variant="default"
                            loading={upload.isPending && upload.variables?.slot === slot}
                          >
                            {src ? t("brands.logoReplace") : t("brands.logoUpload")}
                          </Button>
                        )}
                      </FileButton>
                      {src && (
                        <Button size="compact-sm" variant="subtle" color="red" onClick={() => remove.mutate(slot)}>
                          {t("brands.logoRemove")}
                        </Button>
                      )}
                    </Group>
                    {logoError[slot] && (
                      <Text size="xs" c="red" role="alert">
                        {logoError[slot]}
                      </Text>
                    )}
                  </Stack>
                </Card>
              );
            })}
          </SimpleGrid>
        </Stack>
      </PageSection>

      <PageSection title={t("brands.section.support")}>
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <TextInput
            label={t("brands.support.email")}
            value={values.email}
            onChange={(e) => set("email", e.currentTarget.value)}
            error={fieldErrors.email}
          />
          <TextInput
            label={t("brands.support.phone")}
            value={values.phone}
            maxLength={30}
            onChange={(e) => set("phone", e.currentTarget.value)}
            error={fieldErrors.phone}
          />
          <TextInput
            label={t("brands.support.url")}
            description={t("brands.support.url.help")}
            value={values.url}
            onChange={(e) => set("url", e.currentTarget.value)}
            error={fieldErrors.url}
          />
        </SimpleGrid>
      </PageSection>

      <PageSection title={t("brands.section.preview")}>
        <BrandPreview tokens={tokens} title={values.title || values.name} logoFor={logoUrl} />
      </PageSection>
    </Stack>
  );
};

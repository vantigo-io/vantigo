import {
  Alert,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDeviceFloppy } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type PurchasingAddress,
  type PurchasingSettings,
  purchasingSettingsQueryOptions,
  purchasingSitesQueryOptions,
  savePurchasingSettings,
} from "../api/purchasing";
import { PURCHASING_QUERY_KEY } from "../api/request";
import "../i18n";
import { noPurchasingAccess, type PurchasingAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { usePurchasingFormat } from "../lib/format";

/**
 * Purchasing's settings (P9), behind purchasing:settings: the first order
 * number, the default receiving site, the mail channel orders are sent from,
 * the text printed under an order, the price tolerance and a delivery
 * address per site when it differs from the company's.
 */
export const PurchasingSettingsPage = ({ access = noPurchasingAccess }: { access?: PurchasingAccess }) => {
  const { t } = usePurchasingFormat();
  const settings = useQuery({ ...purchasingSettingsQueryOptions(), refetchOnWindowFocus: false });
  return (
    <Stack gap="lg">
      <PageHeader title={t("settingsTitle")} description={t("settingsDescription")} />
      {settings.isPending && <ContentSkeleton rows={6} rowHeight={48} />}
      {settings.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadSettings")}>
          {refusalMessage(settings.error, t)}
        </Alert>
      )}
      {settings.data && (
        <SettingsForm key={settings.data.revision} settings={settings.data} canSave={access.canManageSettings} />
      )}
    </Stack>
  );
};

const emptyAddress: PurchasingAddress = { name: "", line1: "", line2: "", postalCode: "", city: "", country: "NO" };

const SettingsForm = ({ settings, canSave }: { settings: PurchasingSettings; canSave: boolean }) => {
  const { t } = usePurchasingFormat();
  const queryClient = useQueryClient();
  const sites = useQuery(purchasingSitesQueryOptions());
  const [seriesStart, setSeriesStart] = useState<number | string>(settings.orderSeriesStart);
  const [defaultSiteId, setDefaultSiteId] = useState<string | null>(
    settings.defaultSiteId ? String(settings.defaultSiteId) : null,
  );
  const [channelId, setChannelId] = useState<string | null>(settings.sendChannelId ?? null);
  const [footer, setFooter] = useState(settings.orderFooter);
  const [tolerance, setTolerance] = useState(settings.priceTolerancePercent);
  const [addresses, setAddresses] = useState<Record<number, PurchasingAddress>>(() =>
    Object.fromEntries(settings.deliveryAddresses.map((a) => [a.siteId, a.address])),
  );
  const save = useMutation({
    mutationFn: () =>
      savePurchasingSettings({
        revision: settings.revision,
        orderSeriesStart: Number(seriesStart),
        defaultSiteId: defaultSiteId ? Number(defaultSiteId) : null,
        sendChannelId: channelId,
        orderFooter: footer,
        priceTolerancePercent: tolerance.trim().replace(",", "."),
        deliveryAddresses: Object.entries(addresses).map(([siteId, address]) => ({
          siteId: Number(siteId),
          address: Object.fromEntries(
            Object.entries(address).map(([k, v]) => [k, typeof v === "string" && v.trim() === "" ? null : v]),
          ) as PurchasingAddress,
        })),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [PURCHASING_QUERY_KEY] });
      notifications.show({ color: "green", message: t("saved") });
    },
  });
  const siteList = sites.data?.items ?? [];
  const setAddress = (siteId: number, next: Partial<PurchasingAddress>) =>
    setAddresses((all) => ({ ...all, [siteId]: { ...all[siteId], ...next } }));
  return (
    <Stack gap="lg">
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <NumberInput
            label={t("orderSeriesStart")}
            description={t("nextOrderNumber", { number: settings.nextOrderNumber })}
            min={1}
            allowDecimal={false}
            value={seriesStart}
            onChange={setSeriesStart}
          />
          <Select
            label={t("defaultSiteSetting")}
            clearable
            data={siteList.filter((s) => s.active).map((s) => ({ value: String(s.id), label: s.name }))}
            value={defaultSiteId}
            onChange={setDefaultSiteId}
          />
          <Select
            label={t("sendChannel")}
            description={t("sendChannelHint")}
            clearable
            data={settings.channels.map((c) => ({
              value: c.id,
              label: c.available ? `${c.displayName} <${c.address}>` : t("channelUnavailable", { name: c.displayName }),
            }))}
            value={channelId}
            onChange={setChannelId}
          />
          <TextInput
            label={t("priceTolerance")}
            description={t("priceToleranceHint")}
            inputMode="decimal"
            value={tolerance}
            onChange={(e) => setTolerance(e.currentTarget.value)}
          />
        </SimpleGrid>
        <Textarea
          mt="md"
          label={t("orderFooter")}
          autosize
          minRows={3}
          maxLength={4000}
          value={footer}
          onChange={(e) => setFooter(e.currentTarget.value)}
        />
      </PageSection>
      <PageSection>
        <Stack gap="sm">
          <Title order={4}>{t("deliveryAddresses")}</Title>
          <Text size="sm" c="dimmed">
            {t("deliveryAddressesHint")}
          </Text>
          {siteList.map((site) => {
            const address = addresses[site.id];
            return (
              <Stack key={site.id} gap="xs">
                <Checkbox
                  label={`${site.name} — ${t("ownDeliveryAddress")}`}
                  checked={address !== undefined}
                  onChange={(e) => {
                    const on = e.currentTarget.checked;
                    setAddresses((all) => {
                      const next = { ...all };
                      if (on) next[site.id] = { ...emptyAddress, name: site.name };
                      else delete next[site.id];
                      return next;
                    });
                  }}
                />
                {address && (
                  <SimpleGrid cols={{ base: 1, sm: 3 }} pl="lg">
                    <TextInput
                      label={t("addressName")}
                      value={address.name ?? ""}
                      onChange={(e) => setAddress(site.id, { name: e.currentTarget.value })}
                    />
                    <TextInput
                      label={t("addressLine1")}
                      value={address.line1 ?? ""}
                      onChange={(e) => setAddress(site.id, { line1: e.currentTarget.value })}
                    />
                    <TextInput
                      label={t("addressLine2")}
                      value={address.line2 ?? ""}
                      onChange={(e) => setAddress(site.id, { line2: e.currentTarget.value })}
                    />
                    <TextInput
                      label={t("postalCode")}
                      value={address.postalCode ?? ""}
                      onChange={(e) => setAddress(site.id, { postalCode: e.currentTarget.value })}
                    />
                    <TextInput
                      label={t("city")}
                      value={address.city ?? ""}
                      onChange={(e) => setAddress(site.id, { city: e.currentTarget.value })}
                    />
                    <TextInput
                      label={t("country")}
                      maxLength={2}
                      value={address.country ?? ""}
                      onChange={(e) => setAddress(site.id, { country: e.currentTarget.value.toUpperCase() })}
                    />
                  </SimpleGrid>
                )}
              </Stack>
            );
          })}
        </Stack>
      </PageSection>
      {save.isError && (
        <Alert color="red" title={t("couldNotSave")}>
          {refusalMessage(save.error, t)}
        </Alert>
      )}
      {canSave && (
        <Group justify="flex-end">
          <Button leftSection={<IconDeviceFloppy size={16} />} loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      )}
    </Stack>
  );
};

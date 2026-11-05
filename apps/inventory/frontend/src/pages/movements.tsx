import { Badge, Button, CloseButton, Group, Select, SimpleGrid, Stack } from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { PageHeader, PageSection } from "@vantigo/frontend-shell";
import { inventoryMetaQueryOptions } from "../api/meta";
import { type InventoryMovementKind, movementKinds } from "../api/movements";
import { sitesQueryOptions } from "../api/sites";
import { variantStockQueryOptions } from "../api/stock";
import { LocationPicker } from "../components/location-picker";
import { MovementList } from "../components/movement-list";
import { VariantPicker } from "../components/variant-picker";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import { MOVEMENTS_ROUTE_PATH, newMovementLinkOptions } from "../lib/routes";
import type { MovementsSearch } from "../lib/search";

/**
 * The movements (I30), newest first, filtered by site, location (with what
 * lies below it), variant, kind and date; the filters live in the URL, so a
 * filtered list can be linked to — Receipts in the sidebar is this list with
 * `kind=receipt`.
 */
export const MovementsPage = () => {
  const { t } = useInventoryFormat();
  const search = useSearch({ strict: false }) as MovementsSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const sites = useQuery(sitesQueryOptions());
  const variant = useQuery({ ...variantStockQueryOptions(search.variantId ?? 0), enabled: !!search.variantId });

  const filter = (patch: Partial<MovementsSearch>) =>
    void navigate({ to: MOVEMENTS_ROUTE_PATH, search: { ...search, ...patch }, replace: true });

  return (
    <Stack gap="lg">
      <PageHeader
        title={search.kind === "receipt" ? t("navReceipts") : t("navMovements")}
        description={t("movementsDescription")}
        actions={
          <Group gap="xs">
            {canPost(meta.data, "receipt") && (
              <Button
                variant="light"
                renderRoot={(props) => (
                  <Link {...newMovementLinkOptions("receipt", { siteId: search.siteId })} {...props} />
                )}
              >
                {t("new_receipt")}
              </Button>
            )}
            {canPost(meta.data, "adjustment") && (
              <Button
                variant="light"
                renderRoot={(props) => (
                  <Link {...newMovementLinkOptions("adjustment", { siteId: search.siteId })} {...props} />
                )}
              >
                {t("new_adjustment")}
              </Button>
            )}
            {canPost(meta.data, "move") && (
              <Button
                variant="light"
                renderRoot={(props) => (
                  <Link {...newMovementLinkOptions("move", { siteId: search.siteId })} {...props} />
                )}
              >
                {t("new_move")}
              </Button>
            )}
          </Group>
        }
      />
      <PageSection>
        <SimpleGrid cols={{ base: 1, sm: 2, md: 5 }}>
          <Select
            label={t("site")}
            placeholder={t("allSites")}
            clearable
            data={(sites.data ?? []).map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
            value={search.siteId === undefined ? null : String(search.siteId)}
            onChange={(next) => filter({ siteId: next === null ? undefined : Number(next), locationId: undefined })}
          />
          <LocationPicker
            siteId={search.siteId}
            label={t("location")}
            placeholder={t("allLocations")}
            value={search.locationId ?? null}
            onChange={(next) => filter({ locationId: next ?? undefined })}
          />
          {search.variantId ? (
            <Stack gap={4}>
              <span style={{ fontSize: "var(--mantine-font-size-sm)", fontWeight: 500 }}>{t("variant")}</span>
              <Group gap={4}>
                <Badge variant="light" size="lg">
                  {variant.data?.variant.sku ?? t("variantById", { id: search.variantId })}
                </Badge>
                <CloseButton aria-label={t("clearVariant")} onClick={() => filter({ variantId: undefined })} />
              </Group>
            </Stack>
          ) : (
            <VariantPicker
              label={t("variant")}
              value={undefined}
              onChange={(picked) => filter({ variantId: picked?.id })}
            />
          )}
          <Select
            label={t("kind")}
            placeholder={t("allKinds")}
            clearable
            data={movementKinds.map((value) => ({ value, label: t(`kind_${value}`) }))}
            value={search.kind ?? null}
            onChange={(next) => filter({ kind: (next ?? undefined) as InventoryMovementKind | undefined })}
          />
          <DatePickerInput
            type="range"
            label={t("postedBetween")}
            clearable
            allowSingleDateInRange
            valueFormat={t("dateInputFormat")}
            value={[search.from ?? null, search.to ?? null]}
            onChange={([from, to]) => filter({ from: from ?? undefined, to: to ?? undefined })}
          />
        </SimpleGrid>
      </PageSection>
      <PageSection>
        <MovementList
          filter={{
            siteId: search.siteId,
            locationId: search.locationId,
            variantId: search.variantId,
            kind: search.kind,
            from: search.from,
            to: search.to,
          }}
        />
      </PageSection>
    </Stack>
  );
};

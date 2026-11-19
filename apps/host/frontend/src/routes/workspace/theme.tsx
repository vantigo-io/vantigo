import { Alert, Stack, Tabs, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import {
  brandQueryKey,
  brandRulesQueryKey,
  brandsQueryKey,
  fetchBrand,
  getBrandRules,
  listBrands,
} from "../../api/brands";
import { hasPermissions } from "../../navigation";
import "../../i18n";
import { BrandEditor } from "./-brand-editor";
import { BrandList } from "./-brand-list";
import { BrandLookup } from "./-brand-lookup";
import { BrandRules } from "./-brand-rules";

/**
 * Workspace › Brand (brands design B21): the workspace's brands, the editor of the one being edited, and the
 * rules that say who sees which, with the lookup of who sees what.
 */
const ThemePage = () => {
  const { t } = useI18n("host");
  const [chosen, setChosen] = useState<string | undefined>();
  const list = useQuery({ queryKey: brandsQueryKey, queryFn: listBrands });
  const rules = useQuery({ queryKey: brandRulesQueryKey, queryFn: getBrandRules });
  const items = list.data?.items ?? [];
  // The brand being edited: the one picked in the list, else the default (the list's first).
  const selected = items.find((b) => b.id === chosen) ?? items[0];
  const id = selected?.id;
  const archived = selected?.state === "archived";
  const brand = useQuery({
    queryKey: brandQueryKey(id ?? ""),
    queryFn: () => fetchBrand(id as string),
    enabled: Boolean(id) && !archived,
  });
  const ruleCounts = new Map<string, number>();
  for (const r of rules.data?.items ?? []) ruleCounts.set(r.brandId, (ruleCounts.get(r.brandId) ?? 0) + 1);

  return (
    <Stack gap="md">
      <PageHeader title={t("brands.title")} description={t("brands.intro")} />
      {(list.isError || brand.isError) && <Alert color="red">{t("brands.loadFailed")}</Alert>}
      {list.isPending && <ContentSkeleton />}
      {list.data && (
        <Tabs defaultValue="brands" keepMounted={false}>
          <Tabs.List mb="md">
            <Tabs.Tab value="brands">{t("brands.tab.brands")}</Tabs.Tab>
            <Tabs.Tab value="rules">{t("brands.tab.rules")}</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="brands">
            <Stack gap="lg">
              <BrandList items={items} ruleCounts={ruleCounts} selectedId={id} onSelect={setChosen} />
              {archived && <Alert color="gray">{t("brands.archivedNotice")}</Alert>}
              {id && !archived && brand.isPending && <ContentSkeleton />}
              {brand.data && !archived && (
                <BrandEditor key={`${brand.data.id}-${brand.data.revision}`} brand={brand.data} />
              )}
              {items.length === 0 && <Text c="dimmed">{t("brands.loadFailed")}</Text>}
            </Stack>
          </Tabs.Panel>
          <Tabs.Panel value="rules">
            <Stack gap="lg">
              <BrandRules brands={items} />
              <BrandLookup />
            </Stack>
          </Tabs.Panel>
        </Tabs>
      )}
    </Stack>
  );
};

/** Gated by `identity:brands`, which an Owner holds implicitly (B31). */
export const Route = createFileRoute("/workspace/theme")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (session?.user.roles.includes("Owner")) return;
    const access = session
      ? await context.queryClient.fetchQuery({ queryKey: ["authorization", "me"], queryFn: getAuthorizationMe })
      : undefined;
    if (!hasPermissions(access?.permissions, ["identity:brands"])) throw redirect({ to: "/" });
  },
  component: ThemePage,
});

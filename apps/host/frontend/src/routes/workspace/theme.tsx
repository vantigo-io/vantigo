import { Alert, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { brandQueryKey, brandsQueryKey, fetchBrand, listBrands } from "../../api/brands";
import { hasPermissions } from "../../navigation";
import "../../i18n";
import { BrandEditor } from "./-brand-editor";

/**
 * Workspace › Brand (brands design B21): the editor of the workspace's look. In this delivery there is
 * one brand, the default; the list and the rules come later.
 */
const ThemePage = () => {
  const { t } = useI18n("host");
  const list = useQuery({ queryKey: brandsQueryKey, queryFn: listBrands });
  const id = list.data?.items[0]?.id;
  const brand = useQuery({
    queryKey: brandQueryKey(id ?? ""),
    queryFn: () => fetchBrand(id as string),
    enabled: Boolean(id),
  });
  return (
    <Stack gap="md">
      <PageHeader title={t("brands.title")} description={t("brands.intro")} />
      {(list.isError || brand.isError) && <Alert color="red">{t("brands.loadFailed")}</Alert>}
      {(list.isPending || (id && brand.isPending)) && <ContentSkeleton />}
      {brand.data && <BrandEditor key={`${brand.data.id}-${brand.data.revision}`} brand={brand.data} />}
      {list.data && list.data.items.length === 0 && <Text c="dimmed">{t("brands.loadFailed")}</Text>}
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

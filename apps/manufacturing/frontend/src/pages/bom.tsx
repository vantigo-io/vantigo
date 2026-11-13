import { Alert, Badge, Button, Group, Stack } from "@mantine/core";
import { IconAlertCircle, IconArchive, IconBinaryTree, IconRestore, IconStar } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ContentSkeleton, PageHeader } from "@vantigo/frontend-shell";
import { archiveBom, bomQueryOptions, makeDefaultBom, reactivateBom } from "../api/boms";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { NotFoundError } from "../api/request";
import { BomEditor } from "../components/bom-editor";
import { ReplenishmentRules } from "../components/replenishment-rules";
import { useRecordActions } from "../lib/actions";
import { canManageBoms, canManageOrders } from "../lib/capabilities";
import { statusColor, useManufacturingFormat } from "../lib/format";
import { BOMS_ROUTE_PATH, bomOverviewLinkOptions, MANUFACTURING_HOME_ROUTE_PATH } from "../lib/routes";

const breadcrumbs = (t: ReturnType<typeof useManufacturingFormat>["t"], label: string) => [
  { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
  { label: t("navBoms"), to: BOMS_ROUTE_PATH },
  { label },
];

/** A new BoM's editor. */
export const NewBomPage = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader breadcrumbs={breadcrumbs(t, t("newBom"))} title={t("newBom")} description={t("newBomDescription")} />
      {meta.data && !canManageBoms(meta.data) ? (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />}>
          {t("notAllowed_manageBoms")}
        </Alert>
      ) : meta.isPending ? (
        <ContentSkeleton rows={4} />
      ) : (
        <BomEditor />
      )}
    </Stack>
  );
};

/**
 * One BoM: its editor, and for a manager Make default, Archive and
 * Reactivate, each under the revision read (M10). Reactivating re-runs the
 * cycle walk and makes it the default when its variant has none.
 */
export const BomPage = () => {
  const { t } = useManufacturingFormat();
  const { bomId } = useParams({ strict: false }) as { bomId: string };
  const meta = useQuery(manufacturingMetaQueryOptions());
  const bom = useQuery(bomQueryOptions(Number(bomId)));
  const { run, confirm } = useRecordActions();
  const data = bom.data;
  const manage = canManageBoms(meta.data);
  const name = data ? `${data.variant.sku}${data.code ? ` · ${data.code}` : ""}` : t("bom");

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={breadcrumbs(t, name)}
        title={
          data ? (
            <Group gap="sm">
              {name}
              <Badge variant="light" color={statusColor[data.status]}>
                {t(`status_${data.status}`)}
              </Badge>
              {data.isDefault && (
                <Badge variant="light" color="blue">
                  {t("default")}
                </Badge>
              )}
            </Group>
          ) : (
            name
          )
        }
        description={data?.variant.productName}
        actions={
          data && (
            <Group gap="xs" data-assist="manufacturing.bom.actions">
              <Button
                variant="light"
                leftSection={<IconBinaryTree size={16} />}
                renderRoot={(props) => <Link {...bomOverviewLinkOptions(data.id)} {...props} />}
              >
                {t("openOverview")}
              </Button>
              {manage && data.status === "active" && !data.isDefault && (
                <Button
                  variant="default"
                  leftSection={<IconStar size={16} />}
                  onClick={() =>
                    void run(
                      () => makeDefaultBom(data.id, data.revision),
                      t("bomMadeDefault"),
                      t("couldNotMakeDefault"),
                    )
                  }
                >
                  {t("makeDefault")}
                </Button>
              )}
              {manage && data.status === "active" && (
                <Button
                  variant="default"
                  leftSection={<IconArchive size={16} />}
                  onClick={() =>
                    confirm(
                      t("archiveBomTitle", { name }),
                      t("archiveBomBody"),
                      t("archive"),
                      () => void run(() => archiveBom(data.id, data.revision), t("bomArchived"), t("couldNotArchive")),
                    )
                  }
                >
                  {t("archive")}
                </Button>
              )}
              {manage && data.status === "archived" && (
                <Button
                  variant="default"
                  leftSection={<IconRestore size={16} />}
                  onClick={() =>
                    void run(() => reactivateBom(data.id, data.revision), t("bomReactivated"), t("couldNotReactivate"))
                  }
                >
                  {t("reactivate")}
                </Button>
              )}
            </Group>
          )
        }
      />
      {bom.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {bom.error instanceof NotFoundError ? t("bomNotFound") : bom.error.message}
        </Alert>
      ) : !data || meta.isPending ? (
        <ContentSkeleton rows={4} />
      ) : (
        <>
          {data.status === "archived" && (
            <Alert color="gray" icon={<IconArchive size={16} />}>
              {t("bomArchivedHint")}
            </Alert>
          )}
          {/* A fresh editor per revision: a save, a status change or someone else's edit reloads it. */}
          <BomEditor key={data.revision} bom={data} />
          {data.isDefault && data.status === "active" && (
            <ReplenishmentRules variantId={data.variant.id} editable={canManageOrders(meta.data)} />
          )}
        </>
      )}
    </Stack>
  );
};

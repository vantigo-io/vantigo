import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Menu,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  Title,
} from "@mantine/core";
import {
  IconAdjustments,
  IconAlertCircle,
  IconArchive,
  IconArrowsExchange,
  IconDots,
  IconGridDots,
  IconPackageImport,
  IconPencil,
  IconPlus,
  IconRestore,
  IconTrash,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { locationContentsQueryOptions, locationQueryOptions, siteLocationsQueryOptions } from "../api/locations";
import { inventoryMetaQueryOptions } from "../api/meta";
import { BulkCreateDialog } from "../components/bulk-create-dialog";
import { LocationFormModal } from "../components/location-form";
import { MovementList } from "../components/movement-list";
import { StockTable } from "../components/stock-table";
import { subtreeOf, useStructureActions } from "../lib/actions";
import { canPost } from "../lib/capabilities";
import { useInventoryFormat } from "../lib/format";
import {
  INVENTORY_HOME_ROUTE_PATH,
  locationLinkOptions,
  movementsLinkOptions,
  newMovementLinkOptions,
  siteLinkOptions,
} from "../lib/routes";

type Dialog = "edit" | "child" | "bulk";

const Fact = ({ label, children }: { label: string; children: ReactNode }) => (
  <div>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{children}</Text>
  </div>
);

/**
 * A location (I29): its path as a breadcrumb; its kind, code, full code and
 * status; its children; what lies on it and below it — or on it alone — and
 * the movements touching it, newest first. Adjust, Move from here and Receive
 * here open the forms prefilled; a manager edits, adds below, archives and
 * deletes.
 */
export const LocationPage = () => {
  const { t } = useInventoryFormat();
  const { locationId: rawId } = useParams({ strict: false }) as { locationId: string };
  const locationId = Number(rawId);
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(inventoryMetaQueryOptions());
  const detail = useQuery(locationQueryOptions(locationId));
  const [onlyHere, setOnlyHere] = useState(false);
  const canViewStock = meta.data?.canViewStock === true;
  const canManage = meta.data?.canManageLocations === true;
  const contents = useQuery({ ...locationContentsQueryOptions(locationId, !onlyHere), enabled: canViewStock });
  const [dialog, setDialog] = useState<Dialog>();
  const siteLocations = useQuery({
    ...siteLocationsQueryOptions(detail.data?.site.id ?? 0),
    enabled: dialog === "edit" && detail.data !== undefined,
  });
  const actions = useStructureActions();

  if (detail.error)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
        {detail.error.message}
      </Alert>
    );
  if (detail.isPending) return <ContentSkeleton rows={6} />;

  const { location, site, path, children } = detail.data;
  const active = location.status === "active";
  const root = location.kind === "site";
  const prefill = { siteId: site.id, locationId: location.id };

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[
          { label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH },
          { label: site.code, to: `/inventory/sites/${site.id}` },
          ...path
            .filter((step) => step.kind !== "site")
            .map((step) => ({
              label: step.fullCode.split("-").pop() ?? step.fullCode,
              to: `/inventory/locations/${step.id}`,
            })),
          { label: location.code },
        ]}
        title={
          <Group gap="sm">
            <span>{location.fullCode}</span>
            <Badge variant="light" color="gray">
              {t(`locationKind_${location.kind}`)}
            </Badge>
            {!active && (
              <Badge variant="light" color="yellow">
                {t("status_archived")}
              </Badge>
            )}
          </Group>
        }
        description={location.name}
        actions={
          <Group gap="xs">
            {active && canPost(meta.data, "adjustment") && (
              <Button
                variant="light"
                leftSection={<IconAdjustments size={16} />}
                renderRoot={(props) => <Link {...newMovementLinkOptions("adjustment", prefill)} {...props} />}
              >
                {t("adjust")}
              </Button>
            )}
            {active && canPost(meta.data, "move") && (
              <Button
                variant="light"
                leftSection={<IconArrowsExchange size={16} />}
                renderRoot={(props) => <Link {...newMovementLinkOptions("move", prefill)} {...props} />}
              >
                {t("moveFromHere")}
              </Button>
            )}
            {active && canPost(meta.data, "receipt") && (
              <Button
                variant="light"
                leftSection={<IconPackageImport size={16} />}
                renderRoot={(props) => <Link {...newMovementLinkOptions("receipt", prefill)} {...props} />}
              >
                {t("receiveHere")}
              </Button>
            )}
            {canManage && (
              <Menu position="bottom-end">
                <Menu.Target>
                  <ActionIcon
                    variant="default"
                    size="lg"
                    aria-label={t("locationActions", { code: location.fullCode })}
                  >
                    <IconDots size={16} />
                  </ActionIcon>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => setDialog("edit")}>
                    {t("edit")}
                  </Menu.Item>
                  {active && (
                    <Menu.Item leftSection={<IconPlus size={14} />} onClick={() => setDialog("child")}>
                      {t("addChild")}
                    </Menu.Item>
                  )}
                  {active && (
                    <Menu.Item leftSection={<IconGridDots size={14} />} onClick={() => setDialog("bulk")}>
                      {t("bulkAdd")}
                    </Menu.Item>
                  )}
                  {!root &&
                    (active ? (
                      <Menu.Item
                        leftSection={<IconArchive size={14} />}
                        onClick={() => actions.archiveLocation(location)}
                      >
                        {t("archive")}
                      </Menu.Item>
                    ) : (
                      <Menu.Item
                        leftSection={<IconRestore size={14} />}
                        onClick={() => actions.reactivateLocation(location)}
                      >
                        {t("reactivate")}
                      </Menu.Item>
                    ))}
                  {!root && (
                    <Menu.Item
                      color="red"
                      leftSection={<IconTrash size={14} />}
                      onClick={() => actions.deleteLocation(location, () => void navigate(siteLinkOptions(site.id)))}
                    >
                      {t("delete")}
                    </Menu.Item>
                  )}
                </Menu.Dropdown>
              </Menu>
            )}
          </Group>
        }
      />
      <Card withBorder>
        <SimpleGrid cols={{ base: 2, sm: 4 }}>
          <Fact label={t("site")}>
            <Anchor size="sm" renderRoot={(props) => <Link {...siteLinkOptions(site.id)} {...props} />}>
              {site.code} · {site.name}
            </Anchor>
          </Fact>
          <Fact label={t("code")}>{location.code}</Fact>
          <Fact label={t("fullCode")}>{location.fullCode}</Fact>
          <Fact label={t("status")}>{t(`status_${location.status}`)}</Fact>
        </SimpleGrid>
      </Card>
      <Card withBorder>
        <Stack gap="xs">
          <Title order={4}>{t("children")}</Title>
          {children.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("noChildren")}
            </Text>
          ) : (
            <Group gap="xs">
              {children.map((child) => (
                <Button
                  key={child.id}
                  size="compact-sm"
                  variant={child.status === "active" ? "light" : "subtle"}
                  color={child.status === "active" ? undefined : "gray"}
                  renderRoot={(props) => <Link {...locationLinkOptions(child.id)} {...props} />}
                >
                  {child.code}
                  {child.name ? ` · ${child.name}` : ""}
                </Button>
              ))}
            </Group>
          )}
        </Stack>
      </Card>
      {canViewStock && (
        <Card withBorder>
          <Stack gap="sm">
            <Group justify="space-between">
              <Title order={4}>{t("contents")}</Title>
              <Switch
                label={t("thisLocationOnly")}
                checked={onlyHere}
                onChange={(event) => setOnlyHere(event.currentTarget.checked)}
              />
            </Group>
            {contents.error ? (
              <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
                {contents.error.message}
              </Alert>
            ) : contents.isPending ? (
              <ContentSkeleton rows={3} />
            ) : contents.data.length === 0 ? (
              <EmptyState size="sm" title={t(onlyHere ? "nothingHereOnly" : "nothingHere")} />
            ) : (
              <StockTable rows={contents.data} hideLocation={onlyHere} />
            )}
          </Stack>
        </Card>
      )}
      {canViewStock && (
        <Card withBorder>
          <Stack gap="sm">
            <Group justify="space-between">
              <Title order={4}>{t("navMovements")}</Title>
              <Anchor
                size="sm"
                renderRoot={(props) => (
                  <Link {...movementsLinkOptions({ siteId: site.id, locationId: location.id })} {...props} />
                )}
              >
                {t("allMovements")}
              </Anchor>
            </Group>
            <MovementList filter={{ locationId: location.id }} limit={20} />
          </Stack>
        </Card>
      )}
      {dialog === "edit" && (
        <LocationFormModal
          opened
          onClose={() => setDialog(undefined)}
          siteId={site.id}
          location={location}
          subtree={subtreeOf(siteLocations.data ?? [], location.id)}
        />
      )}
      {dialog === "child" && (
        <LocationFormModal opened onClose={() => setDialog(undefined)} siteId={site.id} parentId={location.id} />
      )}
      {dialog === "bulk" && <BulkCreateDialog opened onClose={() => setDialog(undefined)} parent={location} />}
    </Stack>
  );
};

import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { IconAlertCircle, IconListDetails, IconPlus, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { BOM_PAGE_SIZE, bomsQueryOptions } from "../api/boms";
import { manufacturingMetaQueryOptions } from "../api/meta";
import type { ManufacturingVariantRef } from "../api/variants";
import { statusFilters } from "../api/work-centers";
import { VariantPicker } from "../components/variant-picker";
import { canManageBoms } from "../lib/capabilities";
import { statusColor, useManufacturingFormat } from "../lib/format";
import {
  BOMS_ROUTE_PATH,
  bomLinkOptions,
  bomOverviewLinkOptions,
  MANUFACTURING_HOME_ROUTE_PATH,
  NEW_BOM_ROUTE_PATH,
} from "../lib/routes";
import type { BomsSearch } from "../lib/search";

/**
 * The bills of materials (M10), by the variant they make: filtered by status
 * — active by default — by code, SKU or product name, and by the variant.
 * The filters live in the URL. New BoM needs `manufacturing:boms-manage`.
 */
export const BomsPage = () => {
  const { t, quantity, stamp } = useManufacturingFormat();
  const search = useSearch({ strict: false }) as BomsSearch;
  const navigate = useNavigate() as (options: unknown) => Promise<void>;
  const meta = useQuery(manufacturingMetaQueryOptions());
  const page = search.page ?? 1;
  const boms = useQuery(
    bomsQueryOptions({
      status: search.status,
      q: search.q,
      variantId: search.variantId,
      offset: (page - 1) * BOM_PAGE_SIZE,
    }),
  );
  const [q, setQ] = useState(search.q ?? "");
  // The variant filter shows the variant it names once a row of it has been seen or it was picked here.
  const [variant, setVariant] = useState<ManufacturingVariantRef>();
  const shownVariant =
    search.variantId === undefined
      ? undefined
      : variant?.id === search.variantId
        ? variant
        : boms.data?.items.find((bom) => bom.variant.id === search.variantId)?.variant;

  const filter = (patch: Partial<BomsSearch>) =>
    void navigate({ to: BOMS_ROUTE_PATH, search: { ...search, page: undefined, ...patch }, replace: true });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH }, { label: t("navBoms") }]}
        title={t("navBoms")}
        description={t("bomsDescription")}
        actions={
          canManageBoms(meta.data) && (
            <Button
              data-assist="manufacturing.boms.new"
              leftSection={<IconPlus size={16} />}
              renderRoot={(props) => <Link to={NEW_BOM_ROUTE_PATH} {...props} />}
            >
              {t("newBom")}
            </Button>
          )
        }
      />
      <PageSection>
        <Stack gap="md" data-assist="manufacturing.boms.list">
          <SimpleGrid cols={{ base: 1, sm: 3 }}>
            <Select
              label={t("status")}
              allowDeselect={false}
              data={statusFilters.map((value) => ({ value, label: t(`statusFilter_${value}`) }))}
              value={search.status ?? "active"}
              onChange={(next) =>
                filter({ status: next === null || next === "active" ? undefined : (next as BomsSearch["status"]) })
              }
            />
            <TextInput
              label={t("search")}
              placeholder={t("searchBomsPlaceholder")}
              leftSection={<IconSearch size={16} />}
              value={q}
              onChange={(event) => setQ(event.currentTarget.value)}
              onBlur={() => filter({ q: q.trim() || undefined })}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                event.preventDefault();
                filter({ q: q.trim() || undefined });
              }}
            />
            <VariantPicker
              label={t("variant")}
              value={shownVariant}
              onChange={(next) => {
                setVariant(next);
                filter({ variantId: next?.id });
              }}
            />
          </SimpleGrid>
          {boms.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
              {boms.error.message}
            </Alert>
          ) : boms.isPending ? (
            <ContentSkeleton rows={3} />
          ) : boms.data.items.length === 0 ? (
            <EmptyState
              icon={IconListDetails}
              title={t("noBoms")}
              description={canManageBoms(meta.data) ? t("noBomsHint") : undefined}
            />
          ) : (
            <>
              <Table.ScrollContainer minWidth={720}>
                <Table highlightOnHover>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("variant")}</Table.Th>
                      <Table.Th>{t("bomCode")}</Table.Th>
                      <Table.Th>{t("outputQuantity")}</Table.Th>
                      <Table.Th>{t("columnContents")}</Table.Th>
                      <Table.Th>{t("status")}</Table.Th>
                      <Table.Th>{t("updated")}</Table.Th>
                      <Table.Th />
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {boms.data.items.map((bom) => (
                      <Table.Tr key={bom.id}>
                        <Table.Td>
                          <Anchor renderRoot={(props) => <Link {...bomLinkOptions(bom.id)} {...props} />}>
                            {bom.variant.sku}
                          </Anchor>
                          <Text size="xs" c="dimmed">
                            {bom.variant.productName}
                          </Text>
                        </Table.Td>
                        <Table.Td>{bom.code ?? ""}</Table.Td>
                        <Table.Td>{quantity(bom.outputQuantity, bom.variant.unit)}</Table.Td>
                        <Table.Td>
                          {t("bomContents", { lines: bom.lineCount, operations: bom.operationCount })}
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4}>
                            <Badge variant="light" color={statusColor[bom.status]}>
                              {t(`status_${bom.status}`)}
                            </Badge>
                            {bom.isDefault && (
                              <Badge variant="light" color="blue">
                                {t("default")}
                              </Badge>
                            )}
                            {bom.makeToOrder && (
                              <Badge variant="light" color="grape">
                                {t("makeToOrder")}
                              </Badge>
                            )}
                            {bom.kind === "kit" && (
                              <Badge variant="light" color="teal">
                                {t("bomKind_kit")}
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>{stamp(bom.updatedAt)}</Table.Td>
                        <Table.Td>
                          <Anchor
                            size="sm"
                            renderRoot={(props) => <Link {...bomOverviewLinkOptions(bom.id)} {...props} />}
                          >
                            {t("openOverview")}
                          </Anchor>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
              {boms.data.total > BOM_PAGE_SIZE && (
                <Group justify="flex-end">
                  <Pagination
                    total={Math.ceil(boms.data.total / BOM_PAGE_SIZE)}
                    value={page}
                    onChange={(next) => filter({ page: next > 1 ? next : undefined })}
                  />
                </Group>
              )}
            </>
          )}
        </Stack>
      </PageSection>
    </Stack>
  );
};

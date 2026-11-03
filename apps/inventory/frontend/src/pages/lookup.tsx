import { Alert, Anchor, Badge, Card, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertCircle, IconSearch } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link, Navigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { lookupQueryOptions } from "../api/lookup";
import { InventoryLookupField } from "../components/lookup-field";
import { useInventoryFormat } from "../lib/format";
import { INVENTORY_HOME_ROUTE_PATH, matchLinkOptions } from "../lib/routes";
import type { LookupSearch } from "../lib/search";

/**
 * The lookup's results (I28): one match opens it — the location's page or the
 * variant's stock — several are listed, locations first, and none says so
 * with the text kept in the field to be corrected.
 */
export const LookupPage = () => {
  const { t } = useInventoryFormat();
  const { q = "" } = useSearch({ strict: false }) as LookupSearch;
  const matches = useQuery({ ...lookupQueryOptions(q), enabled: q !== "" });

  if (matches.data?.length === 1) return <Navigate {...matchLinkOptions(matches.data[0])} replace />;

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("inventory"), to: INVENTORY_HOME_ROUTE_PATH }, { label: t("lookupTitle") }]}
        title={t("lookupTitle")}
      />
      {/* Keyed by the text, so a new search from this field starts from what it found. */}
      <InventoryLookupField key={q} initialValue={q} />
      {q === "" ? null : matches.error ? (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
          {matches.error.message}
        </Alert>
      ) : matches.isPending ? (
        <ContentSkeleton rows={3} />
      ) : matches.data.length === 0 ? (
        <EmptyState icon={IconSearch} title={t("lookupNothing", { q })} description={t("lookupNothingHint")} />
      ) : (
        <Card withBorder>
          <Stack gap="xs">
            <Text size="sm" c="dimmed">
              {t("lookupSeveral", { count: matches.data.length, q })}
            </Text>
            <Table verticalSpacing="xs">
              <Table.Tbody>
                {matches.data.map((match, index) => (
                  <Table.Tr key={index}>
                    <Table.Td w={120}>
                      <Badge variant="light" color={match.kind === "location" ? "blue" : "teal"}>
                        {t(`matchKind_${match.kind}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs">
                        <Anchor size="sm" renderRoot={(props) => <Link {...matchLinkOptions(match)} {...props} />}>
                          {match.location?.fullCode ?? match.variant?.sku}
                        </Anchor>
                        <Text size="sm" c="dimmed">
                          {match.location?.name ?? match.variant?.productName}
                        </Text>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        </Card>
      )}
    </Stack>
  );
};

import { Alert, Badge, Button, Card, Group, Select, Stack, Table, Text, Tooltip } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArchive, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  archiveTariff,
  deleteTariff,
  invalidateTariffs,
  type TariffKind,
  type TariffStatus,
  type TariffSummary,
  tariffsQueryOptions,
} from "../api/tariffs";
import { TariffEditor, type TariffEditorState } from "./-tariff-editor";
import "../i18n";

const inUse = (tariff: TariffSummary) => tariff.usage.supplyPeriods + tariff.usage.meteringPoints > 0;

/**
 * **Tariffs** (energy consumption billing design D3, D11): the supplier's
 * power tariffs and the installation's own grid tariffs, with the version
 * valid today and what names each. `canManage` is the host's answer to
 * whether the caller holds `energy:tariffs-manage`; it puts **New tariff**
 * in the header and Edit, Archive and Delete on each row.
 */
export const TariffsPage = ({ canManage = false }: { canManage?: boolean }) => {
  const { t, formatters } = useI18n("energy");
  const client = useQueryClient();
  const [kind, setKind] = useState<TariffKind | null>(null);
  const [status, setStatus] = useState<TariffStatus | null>(null);
  const [editor, setEditor] = useState<TariffEditorState | null>(null);
  const tariffs = useQuery(tariffsQueryOptions({ ...(kind ? { kind } : {}), ...(status ? { status } : {}) }));

  const archive = useMutation({
    mutationFn: archiveTariff,
    onSuccess: (tariff) => {
      void invalidateTariffs(client);
      notifications.show({ color: "teal", title: t("tariffArchived"), message: tariff.name });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotArchiveTariff"), message: error.message }),
  });
  const remove = useMutation({
    mutationFn: (tariff: TariffSummary) => deleteTariff(tariff.id),
    onSuccess: (_, tariff) => {
      void invalidateTariffs(client);
      notifications.show({ color: "teal", title: t("tariffDeleted"), message: tariff.name });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotDeleteTariff"), message: error.message }),
  });

  const confirmArchive = (tariff: TariffSummary) =>
    modals.openConfirmModal({
      title: t("archiveTariffTitle", { name: tariff.name }),
      children: <Text size="sm">{t("archiveTariffConfirm")}</Text>,
      labels: { confirm: t("archiveTariff"), cancel: t("cancel") },
      onConfirm: () => archive.mutate(tariff.id),
    });
  const confirmDelete = (tariff: TariffSummary) =>
    modals.openConfirmModal({
      title: t("deleteTariffTitle", { name: tariff.name }),
      children: <Text size="sm">{t("deleteTariffConfirm")}</Text>,
      labels: { confirm: t("deleteTariff"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(tariff),
    });

  const day = (value: string) => formatters.formatDate(`${value}T12:00:00Z`, { dateStyle: "medium", timeZone: "UTC" });
  const validity = (tariff: TariffSummary) => {
    const current = tariff.currentVersion;
    if (!current) return <Text c="dimmed">{t("tariffNoVersionToday")}</Text>;
    return current.validTo
      ? t("tariffValidRange", { from: day(current.validFrom), to: day(current.validTo) })
      : t("tariffValidFromOpen", { from: day(current.validFrom) });
  };
  const usage = (tariff: TariffSummary) =>
    inUse(tariff)
      ? t("tariffInUseBy", {
          periods: t("tariffSupplyPeriodCount", { count: tariff.usage.supplyPeriods }),
          points: t("tariffMeteringPointCount", { count: tariff.usage.meteringPoints }),
        })
      : t("tariffNotInUse");

  const rows = tariffs.data ?? [];
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("tariffs")}
        description={t("tariffsDescription")}
        actions={
          canManage && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setEditor({ mode: "create" })}>
              {t("newTariff")}
            </Button>
          )
        }
      />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group>
            <Select
              label={t("tariffKind")}
              placeholder={t("tariffAllKinds")}
              clearable
              value={kind}
              onChange={(value) => setKind(value as TariffKind | null)}
              data={[
                { value: "power", label: t("tariffKindPower") },
                { value: "grid", label: t("tariffKindGrid") },
              ]}
            />
            <Select
              label={t("status")}
              placeholder={t("tariffAllStatuses")}
              clearable
              value={status}
              onChange={(value) => setStatus(value as TariffStatus | null)}
              data={[
                { value: "active", label: t("tariffStatusActive") },
                { value: "archived", label: t("tariffStatusArchived") },
              ]}
            />
          </Group>
          {tariffs.error ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTariffs")}>
              {tariffs.error.message}
            </Alert>
          ) : tariffs.isPending ? (
            <ContentSkeleton />
          ) : rows.length === 0 ? (
            <EmptyState size="sm" title={t("noTariffs")} />
          ) : (
            <Table.ScrollContainer minWidth={760}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("tariffName")}</Table.Th>
                    <Table.Th>{t("tariffKind")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                    <Table.Th>{t("tariffValidToday")}</Table.Th>
                    <Table.Th>{t("tariffUsage")}</Table.Th>
                    {canManage && <Table.Th>{t("actions")}</Table.Th>}
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.map((tariff) => (
                    <Table.Tr key={tariff.id}>
                      <Table.Td>
                        <Text fw={500}>{tariff.name}</Text>
                        {tariff.description && (
                          <Text size="xs" c="dimmed">
                            {tariff.description}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td>
                        <Badge variant="light" color={tariff.kind === "power" ? "blue" : "grape"}>
                          {tariff.kind === "power" ? t("tariffKindPower") : t("tariffKindGrid")}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Badge color={tariff.status === "active" ? "teal" : "gray"}>
                          {tariff.status === "active" ? t("tariffStatusActive") : t("tariffStatusArchived")}
                        </Badge>
                      </Table.Td>
                      <Table.Td>{validity(tariff)}</Table.Td>
                      <Table.Td>{usage(tariff)}</Table.Td>
                      {canManage && (
                        <Table.Td>
                          <Group gap="xs" wrap="nowrap">
                            <Button
                              size="compact-sm"
                              variant="light"
                              leftSection={<IconPencil size={14} />}
                              aria-label={t("editTariffNamed", { name: tariff.name })}
                              onClick={() => setEditor({ mode: "edit", tariffId: tariff.id })}
                            >
                              {t("edit")}
                            </Button>
                            {tariff.status === "active" && (
                              <Button
                                size="compact-sm"
                                variant="default"
                                leftSection={<IconArchive size={14} />}
                                aria-label={t("archiveTariffNamed", { name: tariff.name })}
                                onClick={() => confirmArchive(tariff)}
                              >
                                {t("archiveTariff")}
                              </Button>
                            )}
                            <Tooltip label={t("tariffInUseCannotDelete")} disabled={!inUse(tariff)}>
                              <span>
                                <Button
                                  size="compact-sm"
                                  variant="subtle"
                                  color="red"
                                  leftSection={<IconTrash size={14} />}
                                  aria-label={t("deleteTariffNamed", { name: tariff.name })}
                                  disabled={inUse(tariff)}
                                  onClick={() => confirmDelete(tariff)}
                                >
                                  {t("deleteTariff")}
                                </Button>
                              </span>
                            </Tooltip>
                          </Group>
                        </Table.Td>
                      )}
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Stack>
      </Card>
      {canManage && <TariffEditor state={editor} onClose={() => setEditor(null)} />}
    </Stack>
  );
};

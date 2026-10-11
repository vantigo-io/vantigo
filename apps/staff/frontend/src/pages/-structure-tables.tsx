import { ActionIcon, Alert, Badge, Button, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPencil, IconPlus } from "@tabler/icons-react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { personQueryOptions } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import {
  archiveStructureRow,
  type StaffBusinessUnit,
  type StaffLegalEntity,
  type StructureKind,
  structureQueryOptions,
  updateStructureRow,
} from "../api/structure";
import "../i18n";
import { isRevisionConflict, refusalMessage } from "../lib/errors";
import { StructureFormModal, type StructureModalState } from "./-structure-form-modal";

/**
 * Archive and "make default", the two row actions every structure table
 * shares. Neither re-derives a rule: the server refuses archiving the
 * default (`structure_default`) and making an archived row the default
 * (`structure_archived`), and its sentence is what the person is shown.
 */
const useRowActions = (kind: StructureKind) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });

  const archive = useMutation({
    mutationFn: ({ id }: { id: number; name: string }) => archiveStructureRow(kind, id),
    onSuccess: async (_row, { name }) => {
      await refresh();
      notifications.show({ color: "teal", title: t("archivedNotice", { name }), message: "" });
    },
    onError: (error, { name }) =>
      notifications.show({ color: "red", title: t("couldNotArchive", { name }), message: refusalMessage(error) }),
  });

  const makeDefault = useMutation({
    mutationFn: ({ row }: { row: StaffLegalEntity | StaffBusinessUnit }) =>
      // A full replace with the row's own values and the flag; the server
      // takes the flag from the previous default in the same transaction.
      "legalEntityId" in row
        ? updateStructureRow(
            "business-units",
            row.id,
            {
              legalEntityId: row.legalEntityId,
              name: row.name,
              ...(row.orgNumber ? { orgNumber: row.orgNumber } : {}),
              isDefault: true,
            },
            row.revision,
          )
        : updateStructureRow(
            "legal-entities",
            row.id,
            { name: row.name, ...(row.orgNumber ? { orgNumber: row.orgNumber } : {}), isDefault: true },
            row.revision,
          ),
    onSuccess: async (_row, { row }) => {
      await refresh();
      notifications.show({ color: "teal", title: t("nowDefault", { name: row.name }), message: "" });
    },
    onError: async (error, { row }) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await refresh();
        return;
      }
      // structure_archived and the like: the server's own sentence says why.
      notifications.show({
        color: "red",
        title: t("couldNotMakeDefault", { name: row.name }),
        message: refusalMessage(error),
      });
    },
  });

  const confirmArchive = (id: number, name: string) =>
    modals.openConfirmModal({
      title: t("archiveTitle", { name }),
      children: <Text size="sm">{t("archiveExplained")}</Text>,
      labels: { confirm: t("archive"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => archive.mutate({ id, name }),
    });

  return { confirmArchive, makeDefault };
};

interface TableCardProps {
  title: string;
  description: string;
  addLabel: string;
  onAdd: () => void;
  isPending: boolean;
  error: Error | null;
  empty: boolean;
  emptyTitle: string;
  children: ReactNode;
  modal: ReactNode;
}

const TableCard = ({
  title,
  description,
  addLabel,
  onAdd,
  isPending,
  error,
  empty,
  emptyTitle,
  children,
  modal,
}: TableCardProps) => {
  const { t } = useI18n("staff");
  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Stack gap={0} maw={640}>
            <Title order={5}>{title}</Title>
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          </Stack>
          <Button leftSection={<IconPlus size={16} />} onClick={onAdd}>
            {addLabel}
          </Button>
        </Group>
        {error && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadStructure")}>
            {error.message}
          </Alert>
        )}
        {isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {!isPending && !error && empty && <EmptyState title={emptyTitle} description="" />}
        {!empty && <Table.ScrollContainer minWidth={560}>{children}</Table.ScrollContainer>}
        {modal}
      </Stack>
    </Card>
  );
};

/** The default and archived markers, the one column every structure table carries. */
const RowState = ({ isDefault, archivedAt }: { isDefault?: boolean; archivedAt?: string }) => {
  const { t } = useI18n("staff");
  if (archivedAt)
    return (
      <Badge variant="light" color="gray">
        {t("archived")}
      </Badge>
    );
  if (isDefault)
    return (
      <Badge variant="light" color="blue">
        {t("default")}
      </Badge>
    );
  return null;
};

/** An archived row keeps its name and loses its actions: it stays on whatever names it, and nothing more. */
const RowActions = ({
  name,
  archivedAt,
  onEdit,
  onArchive,
  onMakeDefault,
}: {
  name: string;
  archivedAt?: string;
  onEdit: () => void;
  onArchive: () => void;
  onMakeDefault?: () => void;
}) => {
  const { t } = useI18n("staff");
  if (archivedAt) return null;
  return (
    <Group gap={4} wrap="nowrap">
      <ActionIcon variant="subtle" aria-label={t("editRow", { name })} onClick={onEdit}>
        <IconPencil size={16} />
      </ActionIcon>
      {onMakeDefault && (
        <Button size="compact-xs" variant="subtle" aria-label={t("makeDefaultOf", { name })} onClick={onMakeDefault}>
          {t("makeDefault")}
        </Button>
      )}
      <Button size="compact-xs" variant="subtle" color="red" aria-label={t("archiveRow", { name })} onClick={onArchive}>
        {t("archive")}
      </Button>
    </Group>
  );
};

export const LegalEntitiesTable = () => {
  const { t } = useI18n("staff");
  const { data, isPending, error } = useQuery(structureQueryOptions("legal-entities"));
  const { confirmArchive, makeDefault } = useRowActions("legal-entities");
  const [modal, setModal] = useState<StructureModalState | null>(null);
  const rows = data ?? [];

  return (
    <TableCard
      title={t("legalEntities")}
      description={t("legalEntitiesDescription")}
      addLabel={t("addLegalEntity")}
      onAdd={() => setModal({ kind: "legal-entities" })}
      isPending={isPending}
      error={error}
      empty={rows.length === 0}
      emptyTitle={t("noLegalEntities")}
      modal={<StructureFormModal state={modal} onClose={() => setModal(null)} />}
    >
      <Table aria-label={t("legalEntities")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("orgNumber")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
              <Table.Td>{row.name}</Table.Td>
              <Table.Td>{row.orgNumber ?? t("notSet")}</Table.Td>
              <Table.Td>
                <RowState isDefault={row.isDefault} archivedAt={row.archivedAt} />
              </Table.Td>
              <Table.Td>
                <RowActions
                  name={row.name}
                  archivedAt={row.archivedAt}
                  onEdit={() => setModal({ kind: "legal-entities", row })}
                  onArchive={() => confirmArchive(row.id, row.name)}
                  onMakeDefault={row.isDefault ? undefined : () => makeDefault.mutate({ row })}
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </TableCard>
  );
};

export const BusinessUnitsTable = () => {
  const { t } = useI18n("staff");
  const { data, isPending, error } = useQuery(structureQueryOptions("business-units"));
  const { data: entities } = useQuery(structureQueryOptions("legal-entities"));
  const { confirmArchive, makeDefault } = useRowActions("business-units");
  const [modal, setModal] = useState<StructureModalState | null>(null);

  // Each unit under its legal entity: the a-melding reports per unit, and the
  // unit belongs to exactly one juridisk enhet.
  const entityName = (id: number) => entities?.find((entity) => entity.id === id)?.name ?? t("notSet");
  const rows = [...(data ?? [])].sort(
    (a, b) => entityName(a.legalEntityId).localeCompare(entityName(b.legalEntityId)) || a.name.localeCompare(b.name),
  );

  return (
    <TableCard
      title={t("businessUnits")}
      description={t("businessUnitsDescription")}
      addLabel={t("addBusinessUnit")}
      onAdd={() => setModal({ kind: "business-units" })}
      isPending={isPending}
      error={error}
      empty={rows.length === 0}
      emptyTitle={t("noBusinessUnits")}
      modal={<StructureFormModal state={modal} onClose={() => setModal(null)} />}
    >
      <Table aria-label={t("businessUnits")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("legalEntity")}</Table.Th>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("orgNumber")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
              <Table.Td>{entityName(row.legalEntityId)}</Table.Td>
              <Table.Td>{row.name}</Table.Td>
              <Table.Td>{row.orgNumber ?? t("notSet")}</Table.Td>
              <Table.Td>
                <RowState isDefault={row.isDefault} archivedAt={row.archivedAt} />
              </Table.Td>
              <Table.Td>
                <RowActions
                  name={row.name}
                  archivedAt={row.archivedAt}
                  onEdit={() => setModal({ kind: "business-units", row })}
                  onArchive={() => confirmArchive(row.id, row.name)}
                  onMakeDefault={row.isDefault ? undefined : () => makeDefault.mutate({ row })}
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </TableCard>
  );
};

export const DepartmentsTable = () => {
  const { t } = useI18n("staff");
  const { data, isPending, error } = useQuery(structureQueryOptions("departments"));
  const { confirmArchive } = useRowActions("departments");
  const [modal, setModal] = useState<StructureModalState | null>(null);
  const rows = data ?? [];

  // A department names its manager by id; the name is the person's own
  // record. A handful of departments, so one read per manager.
  const managerIds = [...new Set(rows.flatMap((row) => (row.managerPersonId ? [row.managerPersonId] : [])))];
  const managers = useQueries({ queries: managerIds.map((id) => personQueryOptions(id)) });
  const managerName = (id?: number) => {
    if (!id) return t("notSet");
    const query = managers[managerIds.indexOf(id)];
    if (query?.data) return query.data.displayName;
    // A read that failed (a person deleted past the module, a 404) names the
    // id rather than waiting for ever.
    return query?.isError ? t("personById", { id }) : t("loadingName");
  };
  const departmentName = (id?: number) => {
    const found = rows.find((row) => row.id === id);
    return found ? found.name : t("notSet");
  };

  return (
    <TableCard
      title={t("departments")}
      description={t("departmentsDescription")}
      addLabel={t("addDepartment")}
      onAdd={() => setModal({ kind: "departments" })}
      isPending={isPending}
      error={error}
      empty={rows.length === 0}
      emptyTitle={t("noDepartments")}
      modal={<StructureFormModal state={modal} onClose={() => setModal(null)} />}
    >
      <Table aria-label={t("departments")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("code")}</Table.Th>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("manager")}</Table.Th>
            <Table.Th>{t("parentDepartment")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
              <Table.Td>{row.code}</Table.Td>
              <Table.Td>{row.name}</Table.Td>
              <Table.Td>{managerName(row.managerPersonId)}</Table.Td>
              <Table.Td>{departmentName(row.parentId)}</Table.Td>
              <Table.Td>
                <RowState archivedAt={row.archivedAt} />
              </Table.Td>
              <Table.Td>
                <RowActions
                  name={row.name}
                  archivedAt={row.archivedAt}
                  onEdit={() => setModal({ kind: "departments", row })}
                  onArchive={() => confirmArchive(row.id, row.name)}
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </TableCard>
  );
};

export const LocationsTable = () => {
  const { t } = useI18n("staff");
  const { data, isPending, error } = useQuery(structureQueryOptions("locations"));
  const { confirmArchive } = useRowActions("locations");
  const [modal, setModal] = useState<StructureModalState | null>(null);
  const rows = data ?? [];

  return (
    <TableCard
      title={t("locations")}
      description={t("locationsDescription")}
      addLabel={t("addLocation")}
      onAdd={() => setModal({ kind: "locations" })}
      isPending={isPending}
      error={error}
      empty={rows.length === 0}
      emptyTitle={t("noLocations")}
      modal={<StructureFormModal state={modal} onClose={() => setModal(null)} />}
    >
      <Table aria-label={t("locations")}>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("name")}</Table.Th>
            <Table.Th>{t("address")}</Table.Th>
            <Table.Th>{t("country")}</Table.Th>
            <Table.Th>{t("status")}</Table.Th>
            <Table.Th>{t("rowActions")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={row.id} c={row.archivedAt ? "dimmed" : undefined}>
              <Table.Td>{row.name}</Table.Td>
              <Table.Td>
                {[row.street, [row.postalCode, row.city].filter(Boolean).join(" ")].filter(Boolean).join(", ") ||
                  t("notSet")}
              </Table.Td>
              <Table.Td>{row.country}</Table.Td>
              <Table.Td>
                <RowState archivedAt={row.archivedAt} />
              </Table.Td>
              <Table.Td>
                <RowActions
                  name={row.name}
                  archivedAt={row.archivedAt}
                  onEdit={() => setModal({ kind: "locations", row })}
                  onArchive={() => confirmArchive(row.id, row.name)}
                />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </TableCard>
  );
};

import { Alert, Badge, Button, Group, Modal, MultiSelect, Select, Stack, Table, Text, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import { manufacturingMetaQueryOptions } from "../api/meta";
import { isRevisionConflict, refusalCode, refusedFields } from "../api/request";
import { manufacturingSitesQueryOptions } from "../api/sites";
import {
  archiveWorkCenterGroup,
  createWorkCenterGroup,
  type ManufacturingWorkCenterGroup,
  reactivateWorkCenterGroup,
  updateWorkCenterGroup,
  workCenterGroupsQueryOptions,
  workCentersQueryOptions,
} from "../api/work-centers";
import { canManageBoms } from "../lib/capabilities";
import { refusalMessage, statusColor, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

/** Which field a refusal code belongs to, when it is one field's. */
const codeField: Record<string, string> = {
  work_center_code_taken: "code",
  site_unknown: "siteId",
  work_center_unknown: "memberIds",
  work_center_other_site: "memberIds",
};

/**
 * The work center groups (manufacturing phase 2 M117) under Work centers:
 * interchangeable centers of one site an operation may target, planning
 * choosing the member that finishes first. Every group is listed, archived
 * ones too; editing needs `manufacturing:boms-manage`.
 */
export const WorkCenterGroups = () => {
  const { t } = useManufacturingFormat();
  const meta = useQuery(manufacturingMetaQueryOptions());
  const groups = useQuery(workCenterGroupsQueryOptions({ status: "all" }));
  const invalidate = useInvalidateManufacturing();
  const [editing, setEditing] = useState<ManufacturingWorkCenterGroup | "new">();
  const manage = canManageBoms(meta.data);

  const toggle = async (group: ManufacturingWorkCenterGroup) => {
    try {
      if (group.status === "active") await archiveWorkCenterGroup(group.id, group.revision);
      else await reactivateWorkCenterGroup(group.id, group.revision);
      notifications.show({
        color: "teal",
        title: t(group.status === "active" ? "groupArchived" : "groupReactivated"),
        message: group.code,
      });
    } catch (error) {
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error as Error) });
    }
    await invalidate();
  };

  return (
    <PageSection
      title={t("workCenterGroupsTitle")}
      actions={
        manage && (
          <Button variant="light" size="xs" leftSection={<IconPlus size={14} />} onClick={() => setEditing("new")}>
            {t("newWorkCenterGroup")}
          </Button>
        )
      }
    >
      <Stack gap="xs" data-assist="manufacturing.work-centers.groups">
        <Text size="sm" c="dimmed">
          {t("workCenterGroupsHint")}
        </Text>
        {groups.error ? (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotLoad")}>
            {groups.error.message}
          </Alert>
        ) : (groups.data ?? []).length === 0 ? (
          <Text size="sm" c="dimmed">
            {t("noWorkCenterGroups")}
          </Text>
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("code")}</Table.Th>
                  <Table.Th>{t("name")}</Table.Th>
                  <Table.Th>{t("site")}</Table.Th>
                  <Table.Th>{t("groupMembers")}</Table.Th>
                  <Table.Th>{t("status")}</Table.Th>
                  {manage && <Table.Th />}
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(groups.data ?? []).map((group) => (
                  <Table.Tr key={group.id}>
                    <Table.Td>{group.code}</Table.Td>
                    <Table.Td>{group.name}</Table.Td>
                    <Table.Td>{group.siteCode}</Table.Td>
                    <Table.Td>{group.members.map((member) => member.code).join(", ")}</Table.Td>
                    <Table.Td>
                      <Badge variant="light" color={statusColor[group.status]}>
                        {t(`status_${group.status}`)}
                      </Badge>
                    </Table.Td>
                    {manage && (
                      <Table.Td>
                        <Group gap="xs" justify="flex-end" wrap="nowrap">
                          <Button size="xs" variant="default" onClick={() => setEditing(group)}>
                            {t("edit")}
                          </Button>
                          <Button size="xs" variant="subtle" onClick={() => void toggle(group)}>
                            {t(group.status === "active" ? "archive" : "reactivate")}
                          </Button>
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
      <Modal
        opened={editing !== undefined}
        onClose={() => setEditing(undefined)}
        title={
          editing === undefined || editing === "new"
            ? t("newWorkCenterGroup")
            : t("editWorkCenterGroup", { code: editing.code })
        }
      >
        {editing !== undefined && (
          <GroupForm group={editing === "new" ? undefined : editing} onClose={() => setEditing(undefined)} />
        )}
      </Modal>
    </PageSection>
  );
};

/** A group's code, name, site and members, created or saved under its revision. */
const GroupForm = ({ group, onClose }: { group?: ManufacturingWorkCenterGroup; onClose: () => void }) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const sites = useQuery(manufacturingSitesQueryOptions());
  const [code, setCode] = useState(group?.code ?? "");
  const [name, setName] = useState(group?.name ?? "");
  const [siteId, setSiteId] = useState<number | null>(group?.siteId ?? null);
  const [members, setMembers] = useState<string[]>(group?.members.map((member) => String(member.id)) ?? []);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [problem, setProblem] = useState<string>();
  const [sending, setSending] = useState(false);
  const centers = useQuery(workCentersQueryOptions({ status: "active", siteId: siteId ?? undefined }));
  const options = (centers.data ?? []).map((center) => ({
    value: String(center.id),
    label: `${center.code} · ${center.name}`,
  }));
  // A member archived since stays listed, so saving keeps it.
  for (const member of group?.members ?? [])
    if (!options.some((option) => option.value === String(member.id)))
      options.push({ value: String(member.id), label: `${member.code} · ${member.name}` });

  const submit = async () => {
    const found: Record<string, string> = {};
    if (code.trim() === "") found.code = t("problem_required");
    if (name.trim() === "") found.name = t("problem_required");
    if (siteId === null) found.siteId = t("problem_required");
    if (members.length === 0) found.memberIds = t("problem_required");
    setErrors(found);
    if (Object.keys(found).length > 0 || siteId === null) return;
    setSending(true);
    setProblem(undefined);
    const body = { code: code.trim(), name: name.trim(), siteId, memberIds: members.map(Number) };
    try {
      const saved = group
        ? await updateWorkCenterGroup(group.id, { ...body, revision: group.revision })
        : await createWorkCenterGroup(body);
      notifications.show({ color: "teal", title: t("groupSaved"), message: saved.code });
      await invalidate();
      onClose();
    } catch (error) {
      if (isRevisionConflict(error)) await invalidate();
      const shown: Record<string, string> = { ...refusedFields(error) };
      const refused = refusalCode(error);
      if (refused && codeField[refused] && !shown[codeField[refused]])
        shown[codeField[refused]] = t(`refusal.${refused}`);
      setErrors(shown);
      setProblem(refusalMessage(t, error as Error));
    } finally {
      setSending(false);
    }
  };

  return (
    <Stack gap="md">
      <TextInput
        label={t("code")}
        withAsterisk
        maxLength={16}
        value={code}
        onChange={(event) => setCode(event.currentTarget.value.toUpperCase())}
        error={errors.code}
      />
      <TextInput
        label={t("name")}
        withAsterisk
        maxLength={200}
        value={name}
        onChange={(event) => setName(event.currentTarget.value)}
        error={errors.name}
      />
      <Select
        label={t("site")}
        withAsterisk
        data={(sites.data ?? []).map((site) => ({ value: String(site.id), label: `${site.code} · ${site.name}` }))}
        value={siteId === null ? null : String(siteId)}
        onChange={(next) => {
          setSiteId(next === null ? null : Number(next));
          if (next === null || Number(next) !== siteId) setMembers([]);
        }}
        error={errors.siteId}
      />
      <MultiSelect
        label={t("groupMembers")}
        withAsterisk
        searchable
        disabled={siteId === null}
        data={options}
        value={members}
        onChange={setMembers}
        error={errors.memberIds}
      />
      {problem && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          {problem}
        </Alert>
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button loading={sending} onClick={() => void submit()}>
          {t("save")}
        </Button>
      </Group>
    </Stack>
  );
};

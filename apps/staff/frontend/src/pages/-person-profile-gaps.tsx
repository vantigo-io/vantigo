import { Alert, Badge, Button, Group, Modal, MultiSelect, Stack, Table, Text, Title } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconUserCheck } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  competenceGapsQueryOptions,
  competenceProfilesQueryOptions,
  personProfilesQueryOptions,
  replacePersonProfiles,
  type StaffCompetenceGapItem,
  type StaffCompetenceGapProfile,
} from "../api/competences";
import type { StaffPerson } from "../api/people";
import { ApiValidationError, NotFoundError, STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { refusalText } from "../lib/absence";
import { competenceName, gapStatusColor, gapStatusLabel, levelLabel } from "../lib/competences";
import { useStaffFormat } from "../lib/format";

type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * What the person's profiles ask (staff competence design S13, §7): the
 * top of the Competence tab, so the one-to-one starts with the gaps. Every
 * profile that reaches the person — assigned by HR, their department's
 * default, or both — with each item's status as the server judged it on the
 * current rows. There is no capability that says who reads it: the gap list
 * answers the Profiles group's readers and the unknown person's 404 to
 * anyone else, which is the group's absence, so nothing is drawn. HR
 * (`canAssignProfiles`) assigns profiles here; a department's default is set
 * on the department.
 */
export const PersonProfileGaps = ({ person, self }: { person: StaffPerson; self: boolean }) => {
  const { t, locale } = useI18n("staff");
  const gaps = useQuery({
    ...competenceGapsQueryOptions(person.id),
    // The 404 is an answer — not the reader's group — and is not asked again.
    retry: (failures, error) => !(error instanceof NotFoundError) && failures < 3,
  });
  const [assigning, setAssigning] = useState(false);
  const phone = useMediaQuery("(max-width: 48em)");
  const canAssign = person.capabilities.canAssignProfiles;

  if (gaps.isPending || gaps.error instanceof NotFoundError) return null;
  const profileName = (profile: StaffCompetenceGapProfile["profile"]) => {
    const name = competenceName(profile, locale);
    return profile.archived ? t("archivedName", { name }) : name;
  };

  return (
    <FieldGroup
      title={self ? t("whatYourProfileAsks") : t("whatTheirProfileAsks")}
      description={t("profileGapsDescription")}
      actions={
        canAssign && (
          <Button variant="light" leftSection={<IconUserCheck size={16} />} onClick={() => setAssigning(true)}>
            {t("assignProfiles")}
          </Button>
        )
      }
    >
      {gaps.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProfileGaps")}>
          {gaps.error.message}
        </Alert>
      )}
      {gaps.data?.profiles.length === 0 && (
        <EmptyState title={self ? t("noProfileAsks") : t("noProfileAsksThem", { name: person.displayName })} />
      )}
      {gaps.data?.profiles.map((profile) => (
        <GapProfile key={profile.profile.id} profile={profile} name={profileName(profile.profile)} />
      ))}
      {canAssign && (
        <Modal
          opened={assigning}
          onClose={() => setAssigning(false)}
          title={t("assignProfilesTitle", { name: person.displayName })}
          fullScreen={phone}
        >
          {assigning && <AssignForm personId={person.id} onDone={() => setAssigning(false)} />}
        </Modal>
      )}
    </FieldGroup>
  );
};

/** One profile: how it reaches the person, how many of its items are not met, and each item's status. */
const GapProfile = ({ profile, name }: { profile: StaffCompetenceGapProfile; name: string }) => {
  const { t } = useI18n("staff");
  const reaches = [
    ...(profile.assigned ? [t("profileAssigned")] : []),
    ...(profile.department
      ? [t("profileDefaultFor", { department: `${profile.department.name} (${profile.department.code})` })]
      : []),
  ];
  return (
    <Stack gap="xs" component="section" aria-label={name}>
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
        <div>
          <Title order={4} size="h5">
            {name}
          </Title>
          <Text size="sm" c="dimmed">
            {reaches.join(" · ")}
          </Text>
        </div>
        <Badge variant="light" color={profile.notMet > 0 ? "red" : "teal"}>
          {profile.notMet > 0 ? t("gapsNotMet", { count: profile.notMet }) : t("gapsAllMet")}
        </Badge>
      </Group>
      <Table.ScrollContainer minWidth={520}>
        <Table aria-label={t("profileItemsOf", { name })}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("competencePick")}</Table.Th>
              <Table.Th>{t("profileRequirement")}</Table.Th>
              <Table.Th>{t("status")}</Table.Th>
              <Table.Th>{t("profileHeld")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {profile.items.map((item) => (
              <GapRow key={item.competenceId} item={item} />
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Stack>
  );
};

/** What an item asks, in words: a minimum level, and — for a certificate — that it is valid. */
const requirementOf = (item: StaffCompetenceGapItem, t: Translate): string => {
  const parts = [
    ...(item.minLevel !== undefined ? [t("profileAtLeast", { level: levelLabel(t, item.minLevel) })] : []),
    ...(item.mustBeValid && item.competence.kind === "certificate" ? [t("profileValid")] : []),
  ];
  return parts.length > 0 ? parts.join(" · ") : t("profileHolds");
};

const GapRow = ({ item }: { item: StaffCompetenceGapItem }) => {
  const { t, locale } = useI18n("staff");
  const { date } = useStaffFormat();
  const name = competenceName(item.competence, locale);
  const held = [
    ...(item.level !== undefined ? [levelLabel(t, item.level)] : []),
    ...(item.validTo ? [t("competenceValidUntil", { date: date(item.validTo) })] : []),
  ];
  return (
    <Table.Tr data-competence={item.competenceId}>
      <Table.Td>{item.archived ? t("competenceArchivedName", { name }) : name}</Table.Td>
      <Table.Td>{requirementOf(item, t)}</Table.Td>
      <Table.Td>
        <Group gap={4} wrap="wrap">
          <Badge variant="light" color={gapStatusColor(item.status)}>
            {gapStatusLabel(t, item.status)}
          </Badge>
          {item.declared && (
            <Badge variant="outline" color="yellow">
              {t("competenceDeclared")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>{held.length > 0 ? held.join(" · ") : "—"}</Table.Td>
    </Table.Tr>
  );
};

/**
 * HR's assignment: the profiles assigned, replaced whole. The unarchived
 * profiles are offered, and an archived one already assigned stays chosen
 * until HR takes it off. The department's default is not assigned here.
 */
const AssignForm = ({ personId, onDone }: { personId: number; onDone: () => void }) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const catalogue = useQuery(competenceProfilesQueryOptions());
  const reached = useQuery(personProfilesQueryOptions(personId));
  const [chosen, setChosen] = useState<string[] | null>(null);
  const [banner, setBanner] = useState<string[]>([]);
  const assigned = (reached.data?.profiles ?? []).filter((one) => one.assigned).map((one) => String(one.profile.id));
  const value = chosen ?? assigned;
  const options = (catalogue.data ?? [])
    .filter((profile) => !profile.archivedAt || assigned.includes(String(profile.id)))
    .map((profile) => {
      const name = competenceName(profile, locale);
      return { value: String(profile.id), label: profile.archivedAt ? t("archivedName", { name }) : name };
    });
  const labelOf = (id: string | undefined) => options.find((one) => one.value === id)?.label;

  const save = useMutation({
    mutationFn: (ids: string[]) => replacePersonProfiles(personId, ids.map(Number)),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("profilesSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error, ids) => {
      // A refusal at an index names the profile it is about; the list has no field to put it on.
      if (!(error instanceof ApiValidationError)) {
        setBanner([refusalText(error, t)]);
        return;
      }
      const lines = Object.entries(error.fieldErrors).map(([field, message]) => {
        const index = /\[(\d+)\]/.exec(field);
        const what = index ? labelOf(ids[Number(index[1])]) : undefined;
        return what ? t("wishRefusalAt", { what, message }) : message;
      });
      setBanner(lines.length > 0 ? lines : [error.message]);
    },
  });

  return (
    <Stack>
      {banner.length > 0 && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
          <Stack gap={2}>
            {banner.map((line) => (
              <Text key={line} size="sm">
                {line}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
      <Text size="sm" c="dimmed">
        {t("assignProfilesDescription")}
      </Text>
      <MultiSelect
        label={t("competenceProfiles")}
        placeholder={t("profilePickPlaceholder")}
        searchable
        clearable
        maxValues={20}
        data={options}
        value={value}
        onChange={setChosen}
        disabled={!catalogue.data || !reached.data}
      />
      <Group justify="flex-end">
        <Button variant="default" onClick={onDone}>
          {t("cancel")}
        </Button>
        <Button
          loading={save.isPending}
          disabled={!catalogue.data || !reached.data}
          onClick={() => {
            setBanner([]);
            save.mutate(value);
          }}
        >
          {t("saveProfiles")}
        </Button>
      </Group>
    </Stack>
  );
};

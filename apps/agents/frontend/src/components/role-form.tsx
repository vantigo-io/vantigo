import {
  Alert,
  Button,
  Group,
  MultiSelect,
  NativeSelect,
  NumberInput,
  SegmentedControl,
  Slider,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconInfoCircle, IconLock } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  agentQueryKey,
  agentsQueryKey,
  assignableUsersQueryOptions,
  channelsQueryOptions,
  deleteRole,
  putRoleChannels,
  tagsQueryOptions,
  templatesQueryOptions,
  updateRole,
} from "../api/agents";
import { isChannelBound, isVersionConflict } from "../api/request";
import type { Agent, AgentRole, ReplyMode, ScheduleKind } from "../api/types";
import {
  charsLeft,
  clampThreshold,
  INSTRUCTIONS_MAX,
  NAME_MAX,
  NIL_UUID,
  raisesMode,
  replyModes,
  scheduleDays,
  scheduleKinds,
  THRESHOLD_MAX,
  THRESHOLD_MIN,
  WAIT_DAYS_MAX,
  WAIT_DAYS_MIN,
} from "../lib/modes";
import { agentLinkOptions } from "../lib/routes";
import { CalibrationPanel } from "./calibration-panel";
import { ConfirmDialog } from "./confirm-dialog";
import { useErrorText } from "./use-error-text";

/** One of the three modes (replies, starting a conversation, changes): the same three choices, and raising one is the Owner's. */
const ModeField = ({
  id,
  label,
  help,
  value,
  current,
  threshold,
  isOwner,
  canManage,
  onChange,
}: {
  id: string;
  label: string;
  help: string;
  value: ReplyMode;
  current: ReplyMode;
  threshold: number;
  isOwner: boolean;
  canManage: boolean;
  onChange: (value: ReplyMode) => void;
}) => {
  const { t } = useI18n("agents");
  return (
    <Stack gap="xs">
      <Text size="sm" fw={500} id={id}>
        {label}
      </Text>
      <SegmentedControl
        aria-labelledby={id}
        value={value}
        onChange={(next) => onChange(next as ReplyMode)}
        data={replyModes.map((mode) => ({
          value: mode,
          label: t(`replyMode.${mode}`, { threshold: clampThreshold(threshold) }),
          disabled: !isOwner && raisesMode(current, mode),
        }))}
        disabled={!canManage}
      />
      <Text size="xs" c="dimmed">
        {help}
      </Text>
    </Stack>
  );
};

const sameSet = (a: readonly string[], b: readonly string[]) =>
  a.length === b.length && a.every((id) => b.includes(id));

/**
 * One role (V10, screen 3): its name, instructions, hand-off, caps and the
 * template's hard rules, and the fields its template uses (V29, V30): channels,
 * the modes for replies, for starting a conversation and for changes, the wait
 * after a quote, the schedule and the person a digest goes to. The role and its
 * channels are two calls; the role goes first, so a refused mode (the Owner's)
 * leaves the channels as they were.
 */
export const RoleForm = ({
  agent,
  role,
  canManage,
  isOwner,
}: {
  agent: Agent;
  role: AgentRole;
  canManage: boolean;
  isOwner: boolean;
}) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const templates = useQuery(templatesQueryOptions());
  const channels = useQuery(channelsQueryOptions());
  const people = useQuery(assignableUsersQueryOptions());
  const tags = useQuery(tagsQueryOptions());
  const [deleting, { open, close }] = useDisclosure(false);

  const [name, setName] = useState(role.name);
  const [instructions, setInstructions] = useState(role.instructions);
  const [handOff, setHandOff] = useState(role.handOffUserId ?? "");
  const [mode, setMode] = useState<ReplyMode>(role.replyMode);
  const [threshold, setThreshold] = useState(role.threshold);
  const [toolCalls, setToolCalls] = useState<number | string>(role.capsToolCalls);
  const [turns, setTurns] = useState<number | string>(role.capsTurns);
  const [perDay, setPerDay] = useState<number | string>(role.maxRepliesPerDay);
  const [channelIds, setChannelIds] = useState<string[]>(role.channelIds);
  const [complaintTagIds, setComplaintTagIds] = useState<string[]>(role.complaintTagIds ?? []);
  const [startMode, setStartMode] = useState<ReplyMode>(role.startMode);
  const [changeMode, setChangeMode] = useState<ReplyMode>(role.changeMode);
  const [waitDays, setWaitDays] = useState<number | string>(role.waitDays);
  const [recipient, setRecipient] = useState(role.recipientUserId ?? "");
  const [scheduleKind, setScheduleKind] = useState<ScheduleKind>(role.schedule.kind);
  const [scheduleDay, setScheduleDay] = useState(String(role.schedule.day ?? 1));
  const [scheduleTime, setScheduleTime] = useState(role.schedule.time ?? "08:00");

  const template = templates.data?.find((candidate) => candidate.key === role.template);
  const uses = (field: NonNullable<typeof template>["fields"][number]) => template?.fields.includes(field) ?? false;
  const anyModeAuto = [mode, startMode, changeMode].some((value) => value === "auto_above");
  const trimmed = name.trim();
  const numbers = [toolCalls, turns, perDay];
  const valid =
    trimmed !== "" &&
    trimmed.length <= NAME_MAX &&
    instructions.length <= INSTRUCTIONS_MAX &&
    numbers.every((value) => typeof value === "number" && Number.isInteger(value) && value >= 0) &&
    (!uses("waitDays") ||
      (typeof waitDays === "number" &&
        Number.isInteger(waitDays) &&
        waitDays >= WAIT_DAYS_MIN &&
        waitDays <= WAIT_DAYS_MAX)) &&
    (!uses("schedule") || scheduleKind === "none" || /^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(scheduleTime));

  const channelData = channels.isSuccess
    ? channels.data.map((channel) => ({
        value: channel.id,
        label: channel.displayName ? `${channel.displayName} (${channel.address})` : channel.address,
      }))
    : channelIds.map((id) => ({ value: id, label: id }));

  // The tags that mark a complaint (S5); one the list does not hold (still loading, or refused) is kept by its id.
  const tagData = tags.isSuccess
    ? [
        ...tags.data.map((tag) => ({ value: tag.id, label: tag.name })),
        ...complaintTagIds
          .filter((id) => !tags.data.some((tag) => tag.id === id))
          .map((id) => ({ value: id, label: id })),
      ]
    : complaintTagIds.map((id) => ({ value: id, label: id }));

  // The hand-off is a person (V10); a user the list does not hold (still loading, or the list refused) is kept by its id.
  const persons = (people.data ?? []).filter((user) => user.kind === "person");
  const handOffData = [
    { value: "", label: t("handOffInbox") },
    ...persons.map((user) => ({ value: user.userId, label: user.displayName })),
    ...(handOff !== "" && !persons.some((user) => user.userId === handOff) ? [{ value: handOff, label: handOff }] : []),
  ];

  const save = useMutation({
    mutationFn: async () => {
      const saved = await updateRole(
        agent.id,
        role.id,
        {
          name: trimmed,
          instructions,
          handOffUserId: handOff === "" ? null : handOff,
          replyMode: mode,
          threshold: clampThreshold(threshold),
          capsToolCalls: Number(toolCalls),
          capsTurns: Number(turns),
          maxRepliesPerDay: Number(perDay),
          complaintTagIds,
          startMode,
          changeMode,
          waitDays: Number(waitDays),
          recipientUserId: recipient === "" ? NIL_UUID : recipient,
          schedule:
            scheduleKind === "none"
              ? { kind: "none", day: null, time: null }
              : { kind: scheduleKind, day: Number(scheduleDay), time: scheduleTime },
        },
        role.version,
      );
      return sameSet(channelIds, role.channelIds) ? saved : await putRoleChannels(agent.id, role.id, channelIds);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: agentQueryKey(agent.id) });
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("roleSaved"), message: "" });
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
      } else {
        notifications.show({
          color: "red",
          title: t("roleSaveFailed"),
          message: isChannelBound(error) ? t("channelBound") : errorText(error),
        });
      }
      // The role may have moved on, or half of the save (the role itself) may have landed.
      void queryClient.invalidateQueries({ queryKey: agentQueryKey(agent.id) });
    },
  });

  const remove = useMutation({
    mutationFn: () => deleteRole(agent.id, role.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: agentQueryKey(agent.id) });
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("roleDeleted"), message: "" });
      close();
      navigate({ ...agentLinkOptions(agent.id, "roles") });
    },
    onError: (error) => {
      close();
      notifications.show({ color: "red", title: t("roleSaveFailed"), message: errorText(error) });
    },
  });

  const over = charsLeft(instructions, INSTRUCTIONS_MAX) < 0;

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (valid && canManage) save.mutate();
      }}
    >
      <Stack gap="md" maw={720}>
        <TextInput
          label={t("roleNameLabel")}
          value={name}
          onChange={(event) => setName(event.currentTarget.value)}
          maxLength={NAME_MAX}
          disabled={!canManage}
          required
        />

        {uses("channels") && (
          <Stack gap={4}>
            <MultiSelect
              label={t("roleChannels")}
              description={t("roleChannelsHelp")}
              placeholder={t("roleChannelsPlaceholder")}
              data={channelData}
              value={channelIds}
              onChange={setChannelIds}
              searchable
              disabled={!canManage || !isOwner || channels.isPending}
            />
            {canManage && !isOwner && (
              <Text size="xs" c="dimmed">
                {t("ownerRequired")}
              </Text>
            )}
            {channels.isError && (
              <Text size="xs" c="dimmed">
                {t("channelsForbidden")}
              </Text>
            )}
          </Stack>
        )}

        <Stack gap={4}>
          <Textarea
            label={t("roleInstructions")}
            description={t("roleInstructionsHelp")}
            value={instructions}
            onChange={(event) => setInstructions(event.currentTarget.value)}
            autosize
            minRows={8}
            maxRows={24}
            disabled={!canManage}
            error={over ? t("counter", { count: instructions.length, max: INSTRUCTIONS_MAX }) : undefined}
          />
          <Group justify="space-between">
            <Button
              variant="subtle"
              size="compact-sm"
              disabled={!canManage || !template}
              onClick={() => template && setInstructions(template.defaultInstructions)}
            >
              {t("resetInstructions")}
            </Button>
            <Text size="xs" c={over ? "red" : "dimmed"}>
              {t("counter", { count: instructions.length, max: INSTRUCTIONS_MAX })}
            </Text>
          </Group>
        </Stack>

        <Stack gap="xs">
          <NativeSelect
            label={t("handOff")}
            description={t("handOffHelp")}
            value={handOff}
            onChange={(event) => setHandOff(event.currentTarget.value)}
            data={handOffData}
            disabled={!canManage}
          />
          {role.handOffWarning !== "none" && (
            <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
              {t(`handOffWarning.${role.handOffWarning}`)}
            </Alert>
          )}
        </Stack>

        {uses("replyMode") && (
          <Stack gap="xs">
            <Text size="sm" fw={500} id="reply-mode-label">
              {t("replyModeLabel")}
            </Text>
            <SegmentedControl
              aria-labelledby="reply-mode-label"
              value={mode}
              onChange={(value) => setMode(value as ReplyMode)}
              data={replyModes.map((value) => ({
                value,
                label: t(`replyMode.${value}`, { threshold: clampThreshold(threshold) }),
                disabled: !isOwner && raisesMode(role.replyMode, value),
              }))}
              disabled={!canManage}
            />
            <Text size="xs" c="dimmed">
              {t("replyModeHelp")}
            </Text>
            {anyModeAuto && (
              <Stack gap={4}>
                <Text size="sm" id="threshold-label">
                  {t("threshold", { threshold: clampThreshold(threshold) })}
                </Text>
                <Slider
                  aria-labelledby="threshold-label"
                  min={THRESHOLD_MIN}
                  max={THRESHOLD_MAX}
                  step={1}
                  value={threshold}
                  onChange={setThreshold}
                  disabled={!canManage || !isOwner}
                />
              </Stack>
            )}
            <Group gap={6} wrap="nowrap" align="flex-start">
              <IconInfoCircle size={14} />
              <Text size="xs" c="dimmed">
                {(template?.safeOperations.length ?? 0) > 0 ? t("changesMostlyReviewed") : t("changesAlwaysReviewed")}
              </Text>
            </Group>
            <CalibrationPanel roleId={role.id} threshold={threshold} />
          </Stack>
        )}

        {uses("startMode") && (
          <ModeField
            id="start-mode-label"
            label={t("startModeLabel")}
            help={t("startModeHelp")}
            value={startMode}
            current={role.startMode}
            threshold={threshold}
            isOwner={isOwner}
            canManage={canManage}
            onChange={setStartMode}
          />
        )}

        {uses("changeMode") && (template?.safeOperations.length ?? 0) > 0 && (
          <Stack gap={4}>
            <ModeField
              id="change-mode-label"
              label={t("changeModeLabel")}
              help={t("changeModeHelp")}
              value={changeMode}
              current={role.changeMode}
              threshold={threshold}
              isOwner={isOwner}
              canManage={canManage}
              onChange={setChangeMode}
            />
            <Text size="xs" c="dimmed">
              {t("safeOperations", { operations: (template?.safeOperations ?? []).join(", ") })}
            </Text>
          </Stack>
        )}

        {(uses("startMode") || uses("changeMode")) && anyModeAuto && !uses("replyMode") && (
          <Stack gap={4}>
            <Text size="sm" id="threshold-label">
              {t("threshold", { threshold: clampThreshold(threshold) })}
            </Text>
            <Slider
              aria-labelledby="threshold-label"
              min={THRESHOLD_MIN}
              max={THRESHOLD_MAX}
              step={1}
              value={threshold}
              onChange={setThreshold}
              disabled={!canManage || !isOwner}
            />
          </Stack>
        )}

        {uses("waitDays") && (
          <NumberInput
            label={t("waitDays")}
            description={t("waitDaysHelp")}
            value={waitDays}
            onChange={setWaitDays}
            min={WAIT_DAYS_MIN}
            max={WAIT_DAYS_MAX}
            allowDecimal={false}
            disabled={!canManage}
          />
        )}

        {uses("schedule") && (
          <Stack gap="xs">
            <Group grow align="flex-end">
              <NativeSelect
                label={t("scheduleLabel")}
                description={t("scheduleHelp")}
                value={scheduleKind}
                onChange={(event) => {
                  const kind = event.currentTarget.value as ScheduleKind;
                  setScheduleKind(kind);
                  setScheduleDay("1");
                }}
                data={scheduleKinds.map((kind) => ({ value: kind, label: t(`scheduleKind.${kind}`) }))}
                disabled={!canManage}
              />
              {scheduleKind !== "none" && (
                <NativeSelect
                  label={t("scheduleDay")}
                  value={scheduleDay}
                  onChange={(event) => setScheduleDay(event.currentTarget.value)}
                  data={scheduleDays(scheduleKind).map((day) => ({
                    value: String(day),
                    label: scheduleKind === "weekly" ? t(`weekday.${day}`) : String(day),
                  }))}
                  disabled={!canManage}
                />
              )}
              {scheduleKind !== "none" && (
                <TextInput
                  label={t("scheduleTime")}
                  type="time"
                  value={scheduleTime}
                  onChange={(event) => setScheduleTime(event.currentTarget.value)}
                  disabled={!canManage}
                />
              )}
            </Group>
          </Stack>
        )}

        {uses("recipient") && (
          <NativeSelect
            label={t("recipient")}
            description={t("recipientHelp")}
            value={recipient}
            onChange={(event) => setRecipient(event.currentTarget.value)}
            data={[
              { value: "", label: t("recipientNone") },
              ...persons.map((user) => ({ value: user.userId, label: user.displayName })),
              ...(recipient !== "" && !persons.some((user) => user.userId === recipient)
                ? [{ value: recipient, label: recipient }]
                : []),
            ]}
            disabled={!canManage}
          />
        )}

        <MultiSelect
          label={t("complaintTags")}
          description={t("complaintTagsHelp")}
          placeholder={t("complaintTagsPlaceholder")}
          data={tagData}
          value={complaintTagIds}
          onChange={setComplaintTagIds}
          maxValues={20}
          searchable
          disabled={!canManage}
        />

        <Stack gap="xs">
          <Group grow align="flex-start">
            <NumberInput
              label={t("capsToolCalls")}
              value={toolCalls}
              onChange={setToolCalls}
              min={0}
              allowDecimal={false}
              disabled={!canManage}
            />
            <NumberInput
              label={t("capsTurns")}
              value={turns}
              onChange={setTurns}
              min={0}
              allowDecimal={false}
              disabled={!canManage}
            />
            <NumberInput
              label={t("maxRepliesPerDay")}
              value={perDay}
              onChange={setPerDay}
              min={0}
              allowDecimal={false}
              disabled={!canManage}
            />
          </Group>
          <Text size="xs" c="dimmed">
            {t("capsHelp")}
          </Text>
        </Stack>

        {template && (
          <Stack gap="xs">
            <Text size="sm" fw={500}>
              {t("hardRules")}
            </Text>
            <Text size="xs" c="dimmed">
              {t("hardRulesHelp")}
            </Text>
            <Stack gap={6} component="ul" m={0} pl={0} style={{ listStyle: "none" }} data-testid="hard-rules">
              {template.hardRules.map((rule) => (
                <li key={rule.code}>
                  <Group gap={6} wrap="nowrap" align="flex-start">
                    {rule.locked ? <IconLock size={14} aria-hidden /> : <span style={{ width: 14 }} />}
                    <div>
                      <Text size="sm">{t(`rule.${rule.code}`)}</Text>
                      <Text size="xs" c="dimmed">
                        {rule.locked ? t("ruleLockedNote") : t("ruleModeNote")}
                      </Text>
                    </div>
                  </Group>
                </li>
              ))}
            </Stack>
          </Stack>
        )}

        {canManage && (
          <Group justify="space-between">
            <Button type="submit" disabled={!valid} loading={save.isPending}>
              {t("saveRole")}
            </Button>
            <Button variant="subtle" color="red" onClick={open}>
              {t("deleteRole")}
            </Button>
          </Group>
        )}
      </Stack>
      <ConfirmDialog
        opened={deleting}
        title={t("deleteRoleTitle", { name: role.name })}
        body={t("deleteRoleBody")}
        confirmLabel={t("deleteRole")}
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
        onClose={close}
      />
    </form>
  );
};

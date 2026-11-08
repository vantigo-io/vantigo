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
  templatesQueryOptions,
  updateRole,
} from "../api/agents";
import { isChannelBound, isVersionConflict } from "../api/request";
import type { Agent, AgentRole, ReplyMode } from "../api/types";
import {
  charsLeft,
  clampThreshold,
  INSTRUCTIONS_MAX,
  NAME_MAX,
  replyModes,
  THRESHOLD_MAX,
  THRESHOLD_MIN,
  thresholdApplies,
} from "../lib/modes";
import { agentLinkOptions } from "../lib/routes";
import { ConfirmDialog } from "./confirm-dialog";
import { useErrorText } from "./use-error-text";

const sameSet = (a: readonly string[], b: readonly string[]) =>
  a.length === b.length && a.every((id) => b.includes(id));

/**
 * One role (V10, screen 3): its name, channels, instructions, hand-off, reply
 * mode with its threshold, caps and the template's hard rules. The role and its
 * channels are two calls; the role goes first, so a refused mode (the Owner's)
 * leaves the channels as they were.
 */
export const RoleForm = ({ agent, role, canManage }: { agent: Agent; role: AgentRole; canManage: boolean }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const templates = useQuery(templatesQueryOptions());
  const channels = useQuery(channelsQueryOptions());
  const people = useQuery(assignableUsersQueryOptions());
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

  const template = templates.data?.find((candidate) => candidate.key === role.template);
  const trimmed = name.trim();
  const numbers = [toolCalls, turns, perDay];
  const valid =
    trimmed !== "" &&
    trimmed.length <= NAME_MAX &&
    instructions.length <= INSTRUCTIONS_MAX &&
    numbers.every((value) => typeof value === "number" && Number.isInteger(value) && value >= 0);

  const channelData = channels.isSuccess
    ? channels.data.map((channel) => ({
        value: channel.id,
        label: channel.displayName ? `${channel.displayName} (${channel.address})` : channel.address,
      }))
    : channelIds.map((id) => ({ value: id, label: id }));

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

        <Stack gap={4}>
          <MultiSelect
            label={t("roleChannels")}
            description={t("roleChannelsHelp")}
            placeholder={t("roleChannelsPlaceholder")}
            data={channelData}
            value={channelIds}
            onChange={setChannelIds}
            searchable
            disabled={!canManage || channels.isPending}
          />
          {channels.isError && (
            <Text size="xs" c="dimmed">
              {t("channelsForbidden")}
            </Text>
          )}
        </Stack>

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
            }))}
            disabled={!canManage}
          />
          <Text size="xs" c="dimmed">
            {t("replyModeHelp")}
          </Text>
          {thresholdApplies(mode) && (
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
                disabled={!canManage}
              />
              <Text size="xs" c="dimmed">
                {t("notEnoughScored")}
              </Text>
            </Stack>
          )}
          <Group gap={6} wrap="nowrap" align="flex-start">
            <IconInfoCircle size={14} />
            <Text size="xs" c="dimmed">
              {t("changesAlwaysReviewed")}
            </Text>
          </Group>
        </Stack>

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

import { Button, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  isLoggable,
  myOpenTasksQueryOptions,
  myProjectsQueryOptions,
  projectBillingLinesQueryOptions,
} from "../api/projects";
import type { WeekRowRef } from "../lib/rows";
import "../i18n";

export interface RowPickerProps {
  opened: boolean;
  onClose: () => void;
  onAdd: (row: WeekRowRef) => void;
}

/**
 * Adds a row to the week grid (design §8): a project the caller logs time on,
 * then optionally one of its active lines, then optionally one of the
 * caller's open tasks on it. The row lives only on the page until a day in
 * it gets hours.
 */
export const RowPicker = ({ opened, onClose, onAdd }: RowPickerProps) => {
  const { t } = useI18n("time");
  return (
    <Modal opened={opened} onClose={onClose} title={t("addRowTitle")} centered>
      {opened && <RowPickerForm onClose={onClose} onAdd={onAdd} />}
    </Modal>
  );
};

const RowPickerForm = ({ onClose, onAdd }: Omit<RowPickerProps, "opened">) => {
  const { t } = useI18n("time");
  const [projectId, setProjectId] = useState<number | null>(null);
  const [lineId, setLineId] = useState<number | null>(null);
  const [taskId, setTaskId] = useState<number | null>(null);

  const { data: projects } = useQuery(myProjectsQueryOptions());
  const { data: lines } = useQuery({ ...projectBillingLinesQueryOptions(projectId ?? 0), enabled: projectId !== null });
  const { data: tasks } = useQuery(myOpenTasksQueryOptions());

  const loggable = (projects ?? []).filter(isLoggable);
  const project = loggable.find((p) => p.id === projectId);
  const projectTasks = (tasks ?? []).filter((task) => task.projectId === projectId);

  const pickProject = (value: string | null) => {
    setProjectId(value === null ? null : Number(value));
    setLineId(null);
    setTaskId(null);
  };

  const add = () => {
    if (!project) return;
    const line = lines?.find((l) => l.id === lineId);
    const task = projectTasks.find((candidate) => candidate.id === taskId);
    onAdd({
      projectId: project.id,
      projectCode: project.code,
      projectName: project.name,
      billingLineId: line?.id ?? null,
      billingLineCode: line?.code ?? null,
      trackableCode: line?.trackableCode ?? null,
      taskId: task?.id ?? null,
      taskTitle: task?.title ?? null,
    });
    onClose();
  };

  return (
    <Stack>
      {projects && loggable.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noProjects")}
        </Text>
      )}
      <Select
        label={t("project")}
        placeholder={t("chooseProject")}
        searchable
        data-autofocus
        data={loggable.map((p) => ({ value: String(p.id), label: `${p.code} · ${p.name}` }))}
        value={projectId === null ? null : String(projectId)}
        onChange={pickProject}
      />
      <Select
        label={t("billingLine")}
        placeholder={t("noBillingLine")}
        clearable
        disabled={projectId === null || !lines || lines.length === 0}
        data={(lines ?? []).map((line) => ({
          value: String(line.id),
          label: line.productName ? `${line.code} · ${line.productName}` : line.code,
        }))}
        value={lineId === null ? null : String(lineId)}
        onChange={(value) => setLineId(value === null ? null : Number(value))}
      />
      <Select
        label={t("task")}
        placeholder={t("noTask")}
        clearable
        disabled={projectTasks.length === 0}
        data={projectTasks.map((task) => ({ value: String(task.id), label: task.title }))}
        value={taskId === null ? null : String(taskId)}
        onChange={(value) => setTaskId(value === null ? null : Number(value))}
      />
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>
          {t("cancel")}
        </Button>
        <Button onClick={add} disabled={!project}>
          {t("add")}
        </Button>
      </Group>
    </Stack>
  );
};

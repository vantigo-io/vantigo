import { Modal, Select, Stack, Text } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { projectsQueryOptions } from "@vantigo/projects-ui/api/projects";
import { TaskFormModal } from "@vantigo/projects-ui/pages/-task-form-modal";
import { useState } from "react";
import "../../i18n";

/**
 * Spotlight's "Create task" action, which knows no project: a picker over the
 * caller's own projects, and then the package's own task form on the one they
 * chose. It lives in the host because picking the project is a host concern —
 * the package's form takes a `projectId` and asks no questions about it.
 */
export const CreateTaskModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("host");
  const [projectId, setProjectId] = useState<number | null>(null);
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, 300);
  // The caller's own projects: a task is created where the caller works, and
  // `mine` is the same filter the projects list's "My projects" toggle uses.
  const projects = useQuery({
    ...projectsQueryOptions({ page: 1, search: debouncedSearch.trim(), status: "", mine: true }),
    enabled: opened,
  });

  const close = () => {
    setProjectId(null);
    setSearch("");
    onClose();
  };

  return (
    <>
      <Modal opened={opened && projectId === null} onClose={close} title={t("project.createTaskTitle")} centered>
        <Stack>
          <Select
            label={t("project.taskProject")}
            placeholder={t("project.taskProjectPlaceholder")}
            data-autofocus
            searchable
            // The search runs on the server, so Mantine must not filter the
            // rows it is given a second time.
            filter={({ options }) => options}
            searchValue={search}
            onSearchChange={setSearch}
            // Silent while the answer is still on its way, rather than
            // telling the caller they have no projects before asking.
            nothingFoundMessage={projects.isPending ? undefined : t("project.noProjectsFound")}
            data={(projects.data?.data ?? []).map((project) => ({
              value: String(project.id),
              label: `${project.code} — ${project.name}`,
            }))}
            value={projectId === null ? null : String(projectId)}
            onChange={(value) => value && setProjectId(Number(value))}
          />
          <Text size="sm" c="dimmed">
            {t("project.taskProjectHint")}
          </Text>
        </Stack>
      </Modal>
      {projectId !== null && <TaskFormModal projectId={projectId} state={{ mode: "create" }} onClose={close} />}
    </>
  );
};

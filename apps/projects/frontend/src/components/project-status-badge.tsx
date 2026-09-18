import { Badge, type MantineSize } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { type ProjectStatus, projectStatusColor, projectStatusLabelKey } from "../lib/status";

export interface ProjectStatusBadgeProps {
  status: ProjectStatus;
  size?: MantineSize;
}

/** A project's status, in the one colour and wording every page uses for it. */
export const ProjectStatusBadge = ({ status, size }: ProjectStatusBadgeProps) => {
  const { t } = useI18n("projects");
  return (
    <Badge variant="light" color={projectStatusColor(status)} size={size}>
      {t(projectStatusLabelKey(status))}
    </Badge>
  );
};

import { Alert, Avatar, Tooltip } from "@mantine/core";
import { IconPencil } from "@tabler/icons-react";
import { type PresenceMember, useI18n } from "@vantigo/frontend-shell";

const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("");

/**
 * "Kari is editing" above the editor while someone else's presence on the
 * article is `editing` (knowledge design K9). It warns; it does not lock —
 * the version token is what keeps two saves apart.
 */
export const EditingBanner = ({ members }: { members: readonly PresenceMember[] }) => {
  const { t } = useI18n("knowledge");
  const editing = members.filter((member) => member.state === "editing").map((member) => member.name);
  if (editing.length === 0) return null;
  const message =
    editing.length === 1
      ? t("presenceEditingOne", { name: editing[0] })
      : t("presenceEditingMany", { names: editing.slice(0, -1).join(", "), last: editing.at(-1) });
  return (
    <Alert color="yellow" variant="light" icon={<IconPencil size={16} />} role="status" py="xs">
      {message}
    </Alert>
  );
};

/** The others on the page, as avatars with their names. */
export const PresenceAvatars = ({ members }: { members: readonly PresenceMember[] }) => {
  const { t } = useI18n("knowledge");
  if (members.length === 0) return null;
  return (
    <Tooltip.Group openDelay={200}>
      <Avatar.Group spacing="sm" aria-label={t("presenceAlsoHere")}>
        {members.slice(0, 5).map((member) => (
          <Tooltip
            key={member.userId}
            label={t(member.state === "editing" ? "presenceMemberEditing" : "presenceMemberViewing", {
              name: member.name,
            })}
          >
            <Avatar size="sm" radius="xl" color="initials" name={member.name} aria-label={member.name}>
              {initials(member.name)}
            </Avatar>
          </Tooltip>
        ))}
        {members.length > 5 && (
          <Avatar size="sm" radius="xl">
            +{members.length - 5}
          </Avatar>
        )}
      </Avatar.Group>
    </Tooltip.Group>
  );
};

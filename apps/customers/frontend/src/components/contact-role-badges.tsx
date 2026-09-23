import { Badge, Group, Tooltip } from "@mantine/core";
import { IconStarFilled } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ContactRoleAssignment } from "../api/contacts";
import { contactRoleLabel } from "../lib/contact-role-label";
import "../i18n";

/**
 * A contact's typed roles for one customer, as chips (design D5) — the same
 * one-component treatment `TagBadge` gets, and for the same reason: the
 * customer's contacts card and the contact page's customers card show the
 * identical thing, and two copies of three props drift.
 *
 * The primary one carries a star and, more importantly, an `aria-label`
 * spelling out what the star means — "Primary billing contact". A tooltip alone
 * would leave the fact to a hover, which is not where anyone using a screen
 * reader or a phone will find it.
 *
 * The array arrives in the fixed order billing, project, decision_maker (the
 * server sorts it, and `normalizeContactRoles` sorts it again), so nothing here
 * orders anything.
 */
export const ContactRoleBadges = ({ roles }: { roles: ContactRoleAssignment[] }) => {
  const { t } = useI18n("customers");
  if (roles.length === 0) return null;

  return (
    <Group gap={4} wrap="wrap">
      {roles.map((assignment) => {
        const label = contactRoleLabel(t, assignment.role);
        if (!assignment.primary) {
          return (
            <Badge key={assignment.role} variant="light" color="gray" size="sm" tt="none" fw={500}>
              {label}
            </Badge>
          );
        }
        const primaryLabel = t("primaryRoleFor", { role: label.toLocaleLowerCase() });
        return (
          <Tooltip key={assignment.role} label={primaryLabel}>
            <Badge
              variant="light"
              color="blue"
              size="sm"
              tt="none"
              fw={500}
              aria-label={primaryLabel}
              leftSection={<IconStarFilled size={10} />}
            >
              {label}
            </Badge>
          </Tooltip>
        );
      })}
    </Group>
  );
};

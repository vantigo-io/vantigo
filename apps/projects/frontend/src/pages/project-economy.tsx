import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Group,
  Menu,
  SimpleGrid,
  Stack,
  Table,
  Text,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconDots, IconInfoCircle, IconLock, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  type BillingMilestone,
  type BillingMilestoneTotals,
  deleteMilestone,
  milestonePlanQueryOptions,
  moveMilestone,
  setMilestoneStatus,
} from "../api/milestones";
import { type Project, projectQueryOptions } from "../api/projects";
import type { ApiError } from "../api/request";
import { ApiValidationError } from "../api/request";
import { Field } from "../components/field";
import "../i18n";
import { useProjectDates } from "../lib/dates";
import { type MilestoneStatus, milestoneStatusColor, milestoneStatusLabelKey } from "../lib/milestones";
import { MilestoneFormModal, type MilestoneModalState } from "./-milestone-form-modal";
import { MilestoneInvoicedModal } from "./-milestone-invoiced-modal";
import { ProjectFormModal, type ProjectModalState } from "./-project-form-modal";

/**
 * An amount as this project writes it: in the project's currency when it has
 * one, and the catalog's dash — never a zero — for an amount the API left out.
 */
const useMoney = () => {
  const { t, formatters } = useI18n("projects");
  return (value: number | null | undefined, currency: string | undefined): string =>
    value === null || value === undefined
      ? t("notAvailable")
      : currency
        ? formatters.formatCurrency(value, currency)
        : formatters.formatNumber(value);
};

/**
 * The Economy tab (design §7). Delivery A fills it with the invoice plan,
 * which is financial data throughout: the API answers 403 to a caller who may
 * see the project but not its amounts, so the tab asks the project first and
 * never asks for a plan it would only be refused.
 */
export const ProjectEconomy = ({ projectId }: { projectId: number }) => {
  const { t } = useI18n("projects");
  const { data: project, isPending, isError, error } = useQuery(projectQueryOptions(projectId));

  if (isError) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadProject")} mt="md">
        {error.message}
      </Alert>
    );
  }
  if (isPending)
    return (
      <Box mt="md">
        <ContentSkeleton rows={4} rowHeight={48} />
      </Box>
    );

  if (!project.capabilities.canSeeFinancials) {
    return (
      <Box mt="md">
        <EmptyState icon={IconLock} title={t("financialsHidden")} description={t("invoicePlanHiddenDescription")} />
      </Box>
    );
  }

  return (
    <Stack gap="lg" mt="md">
      <InvoicePlan projectId={projectId} project={project} />
    </Stack>
  );
};

const InvoicePlan = ({ projectId, project }: { projectId: number; project: Project }) => {
  const { t } = useI18n("projects");
  const { data: plan, isPending, isError, error } = useQuery(milestonePlanQueryOptions(projectId));
  const [modalState, setModalState] = useState<MilestoneModalState | null>(null);
  const [invoicing, setInvoicing] = useState<BillingMilestone | null>(null);
  const [projectModal, setProjectModal] = useState<ProjectModalState | null>(null);

  const currency = project.financials?.currency ?? undefined;
  const fixedPrice = project.financials?.fixedPriceAmount ?? undefined;
  const canManage = project.capabilities.canManageMilestones;
  const milestones = plan?.milestones ?? [];
  // Cancelled rows are listed last but keep their stored numbers, so the plan's
  // order is the array's order and the reordering targets are read off the
  // rows that are still open.
  const open = milestones.filter((milestone) => milestone.status !== "cancelled");

  return (
    <>
      {plan && <HeadlineFigures totals={plan.totals} />}

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group justify="space-between" wrap="wrap">
            <Stack gap={2}>
              <Text fw={600} component="h3">
                {t("invoicePlan")}
              </Text>
              <Text size="sm" c="dimmed">
                {t("invoicePlanDescription")}
              </Text>
            </Stack>
            {canManage && currency && (
              <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setModalState({ mode: "create" })}>
                {t("addMilestone")}
              </Button>
            )}
          </Group>

          {!currency && (
            <Alert color="yellow" icon={<IconInfoCircle size={16} />}>
              <Group justify="space-between" wrap="wrap" gap="xs">
                <Text size="sm">{t("milestonesNeedCurrency")}</Text>
                {project.capabilities.canManage && (
                  <Button size="compact-xs" variant="light" onClick={() => setProjectModal({ mode: "edit", project })}>
                    {t("editTheProject")}
                  </Button>
                )}
              </Group>
            </Alert>
          )}

          {isError && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadInvoicePlan")}>
              {error.message}
            </Alert>
          )}
          {isPending && <ContentSkeleton rows={3} rowHeight={48} />}
          {plan && milestones.length === 0 && <EmptyState title={t("noMilestones")} size="sm" />}

          {milestones.length > 0 && (
            <Table.ScrollContainer minWidth={760}>
              <Table striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("milestoneName")}</Table.Th>
                    <Table.Th>{t("plannedDate")}</Table.Th>
                    <Table.Th>{t("amount")}</Table.Th>
                    <Table.Th>{t("status")}</Table.Th>
                    <Table.Th>{t("actions")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {milestones.map((milestone) => {
                    const at = open.indexOf(milestone);
                    return (
                      <MilestoneRow
                        key={milestone.id}
                        milestone={milestone}
                        canManage={canManage}
                        moveUpTo={at > 0 ? open[at - 1].position : undefined}
                        moveDownTo={at >= 0 && at < open.length - 1 ? open[at + 1].position : undefined}
                        onEdit={setModalState}
                        onInvoice={setInvoicing}
                      />
                    );
                  })}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}

          {plan && <PlanFooter totals={plan.totals} />}
        </Stack>
      </Card>

      <MilestoneFormModal
        projectId={projectId}
        fixedPrice={fixedPrice}
        currency={currency}
        state={modalState}
        onClose={() => setModalState(null)}
      />
      <MilestoneInvoicedModal milestone={invoicing} onClose={() => setInvoicing(null)} />
      <ProjectFormModal state={projectModal} onClose={() => setProjectModal(null)} />
    </>
  );
};

/** What the plan stands at, in the three figures somebody planning invoices reads first. */
const HeadlineFigures = ({ totals }: { totals: BillingMilestoneTotals }) => {
  const { t } = useI18n("projects");
  const money = useMoney();
  const currency = totals.currency ?? undefined;

  return (
    <Card withBorder padding="lg" radius="md" data-testid="milestone-totals">
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
        <Field label={t("plannedTotal")}>{money(totals.planned, currency)}</Field>
        <Field label={t("readyTotal")}>{money(totals.ready, currency)}</Field>
        <Field label={t("invoicedTotal")}>{money(totals.invoiced, currency)}</Field>
      </SimpleGrid>
    </Card>
  );
};

/**
 * The fixed price the plan is read against, and the one sentence that says how
 * far off it is. Over-planning is a thing to look at rather than a failure, so
 * it is a quiet note and never an error.
 */
const PlanFooter = ({ totals }: { totals: BillingMilestoneTotals }) => {
  const { t } = useI18n("projects");
  const money = useMoney();
  const currency = totals.currency ?? undefined;
  if (totals.fixedPrice === undefined || totals.fixedPrice === null) return null;

  return (
    <Stack gap="xs" data-testid="milestone-plan-footer">
      <Group gap="xs">
        <Text size="sm" c="dimmed">
          {t("fixedPriceAmount")}
        </Text>
        <Text size="sm" fw={600}>
          {money(totals.fixedPrice, currency)}
        </Text>
      </Group>
      {totals.unplanned !== undefined && totals.unplanned !== null && (
        <Text size="sm" c="dimmed">
          {t("milestonesUnplanned", { amount: money(totals.unplanned, currency) })}
        </Text>
      )}
      {totals.overPlanned !== undefined && totals.overPlanned !== null && (
        <Alert color="yellow" icon={<IconInfoCircle size={16} />} data-testid="over-planned-note">
          {t("milestonesOverPlanned", { amount: money(totals.overPlanned, currency) })}
        </Alert>
      )}
    </Stack>
  );
};

/** How a milestone is priced, in words: a flat amount, or a share and what it comes to. */
const MilestoneAmount = ({ milestone }: { milestone: BillingMilestone }) => {
  const { t, formatters } = useI18n("projects");
  const money = useMoney();
  const currency = milestone.currency ?? undefined;
  const amount = money(milestone.effectiveAmount, currency);
  if (milestone.percent === undefined || milestone.percent === null) return <Text size="sm">{amount}</Text>;
  return (
    <Text size="sm">
      {t("milestonePercentAmount", { percent: formatters.formatNumber(milestone.percent), amount })}
    </Text>
  );
};

const MilestoneRow = ({
  milestone,
  canManage,
  moveUpTo,
  moveDownTo,
  onEdit,
  onInvoice,
}: {
  milestone: BillingMilestone;
  /** The project's `capabilities.canManageMilestones` — who may reorder the plan. */
  canManage: boolean;
  /** The position a "move up" asks for, absent when the row is already first among the open ones. */
  moveUpTo?: number;
  moveDownTo?: number;
  onEdit: (state: MilestoneModalState) => void;
  onInvoice: (milestone: BillingMilestone) => void;
}) => {
  const { t } = useI18n("projects");
  const dates = useProjectDates();
  const cancelled = milestone.status === "cancelled";
  const actions = useMilestoneActions(milestone);

  const items: ReactNode[] = [];
  const can = milestone.capabilities;
  if (can.canEdit) {
    items.push(
      <Menu.Item key="edit" onClick={() => onEdit({ mode: "edit", milestone })}>
        {t("edit")}
      </Menu.Item>,
    );
  }
  // Reordering is the project's right, not the milestone's, and a cancelled
  // row has no place in the order to move within.
  if (canManage && !cancelled && moveUpTo !== undefined) {
    items.push(
      <Menu.Item key="up" onClick={() => actions.move(moveUpTo)}>
        {t("moveMilestoneUp")}
      </Menu.Item>,
    );
  }
  if (canManage && !cancelled && moveDownTo !== undefined) {
    items.push(
      <Menu.Item key="down" onClick={() => actions.move(moveDownTo)}>
        {t("moveMilestoneDown")}
      </Menu.Item>,
    );
  }
  if (can.canMarkReady) {
    items.push(
      <Menu.Item key="ready" onClick={() => actions.moveTo("ready")}>
        {t("markMilestoneReady")}
      </Menu.Item>,
    );
  }
  if (can.canMarkPlanned) {
    items.push(
      <Menu.Item key="planned" onClick={() => actions.moveTo("planned")}>
        {t("markMilestonePlanned")}
      </Menu.Item>,
    );
  }
  if (can.canMarkInvoiced) {
    items.push(
      <Menu.Item key="invoiced" onClick={() => onInvoice(milestone)}>
        {t("markMilestoneInvoiced")}
      </Menu.Item>,
    );
  }
  if (can.canUndoInvoiced) {
    items.push(
      <Menu.Item key="undo" onClick={actions.confirmUndo}>
        {t("undoMilestoneInvoiced")}
      </Menu.Item>,
    );
  }
  if (can.canReopen) {
    items.push(
      <Menu.Item key="reopen" onClick={() => actions.moveTo("planned")}>
        {t("reopenMilestone")}
      </Menu.Item>,
    );
  }
  if (can.canCancel) {
    items.push(
      <Menu.Item key="cancel" color="red" onClick={actions.confirmCancel}>
        {t("cancelMilestone")}
      </Menu.Item>,
    );
  }
  if (can.canDelete) {
    items.push(
      <Menu.Item key="delete" color="red" onClick={actions.confirmDelete}>
        {t("deleteMilestone")}
      </Menu.Item>,
    );
  }

  return (
    <Table.Tr>
      <Table.Td>
        {/* The description is written out rather than hidden in a tooltip: a
            viewer with financial rights may not edit a milestone, so the row
            is the only place they would ever read what it is for. */}
        <Stack gap={2}>
          <Text
            size="sm"
            fw={500}
            data-testid="milestone-name"
            td={cancelled ? "line-through" : undefined}
            c={cancelled ? "dimmed" : undefined}
          >
            {milestone.name}
          </Text>
          {milestone.description && (
            <Text size="xs" c="dimmed" lineClamp={2} maw={360}>
              {milestone.description}
            </Text>
          )}
        </Stack>
      </Table.Td>
      <Table.Td>
        <Group gap="xs" wrap="nowrap">
          <Text size="sm">{milestone.plannedDate ? dates.day(milestone.plannedDate) : t("notAvailable")}</Text>
          {milestone.overdue && (
            <Badge variant="light" color="red" size="sm">
              {t("overdue")}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        <MilestoneAmount milestone={milestone} />
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color={milestoneStatusColor(milestone.status)}>
          {t(milestoneStatusLabelKey(milestone.status))}
        </Badge>
      </Table.Td>
      <Table.Td>
        {items.length > 0 && (
          <Menu position="bottom-end" withinPortal>
            <Menu.Target>
              {/* Named after its own row: a reader listing the page's buttons
                  gets one per milestone, not six of the same. */}
              <ActionIcon
                variant="subtle"
                aria-label={t("milestoneActionsFor", { name: milestone.name })}
                loading={actions.isPending}
              >
                <IconDots size={16} />
              </ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>{items}</Menu.Dropdown>
          </Menu>
        )}
      </Table.Td>
    </Table.Tr>
  );
};

/**
 * Every write one row can make. A refused status move arrives as a 400 on
 * `status` and a revision conflict as the module's own project wording, so
 * both are turned into something a reader of this plan can act on rather than
 * being swallowed.
 */
const useMilestoneActions = (milestone: BillingMilestone) => {
  const { t } = useI18n("projects");
  const queryClient = useQueryClient();

  const report = (title: string) => (error: Error) => {
    if (error instanceof ApiValidationError) {
      const message = error.fieldErrors.status ?? Object.values(error.fieldErrors)[0] ?? error.message;
      notifications.show({ color: "red", title, message });
      return;
    }
    const conflict = (error as ApiError).status === 409;
    notifications.show({ color: "red", title, message: conflict ? t("milestoneChangedElsewhere") : error.message });
  };

  const done = (title: string) => () => {
    queryClient.invalidateQueries({ queryKey: ["projects"] });
    notifications.show({ color: "teal", title, message: milestone.name });
  };

  const status = useMutation({
    mutationFn: (to: MilestoneStatus) => setMilestoneStatus(milestone.id, { status: to, revision: milestone.revision }),
    onSuccess: done(t("milestoneUpdated")),
    onError: report(t("couldNotChangeMilestone")),
  });

  const move = useMutation({
    mutationFn: (position: number) => moveMilestone(milestone.id, { position, revision: milestone.revision }),
    onSuccess: done(t("planReordered")),
    onError: report(t("couldNotReorderPlan")),
  });

  const remove = useMutation({
    mutationFn: () => deleteMilestone(milestone.id),
    onSuccess: done(t("milestoneDeleted")),
    onError: report(t("couldNotDeleteMilestone")),
  });

  return {
    isPending: status.isPending || move.isPending || remove.isPending,
    moveTo: (to: MilestoneStatus) => status.mutate(to),
    move: (position: number) => move.mutate(position),
    confirmCancel: () =>
      modals.openConfirmModal({
        title: t("cancelMilestoneTitle", { name: milestone.name }),
        children: <Text size="sm">{t("cancelMilestoneWarning")}</Text>,
        labels: { confirm: t("cancelMilestone"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => status.mutate("cancelled"),
      }),
    confirmUndo: () =>
      modals.openConfirmModal({
        title: t("undoMilestoneInvoicedTitle", { name: milestone.name }),
        children: <Text size="sm">{t("undoMilestoneInvoicedWarning")}</Text>,
        labels: { confirm: t("undoMilestoneInvoiced"), cancel: t("cancel") },
        onConfirm: () => status.mutate("ready"),
      }),
    confirmDelete: () =>
      modals.openConfirmModal({
        title: t("deleteMilestoneTitle", { name: milestone.name }),
        children: <Text size="sm">{t("deleteMilestoneWarning")}</Text>,
        labels: { confirm: t("deleteMilestone"), cancel: t("cancel") },
        confirmProps: { color: "red" },
        onConfirm: () => remove.mutate(),
      }),
  };
};

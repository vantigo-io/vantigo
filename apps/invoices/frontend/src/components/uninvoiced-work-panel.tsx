import { Alert, Badge, Button, Card, Checkbox, Group, Stack, Table, Text, Title } from "@mantine/core";
import { IconAlertCircle, IconFileInvoice } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { consumptionQueryOptions, type DueSupply } from "../api/consumption";
import { invoicesMetaQueryOptions } from "../api/meta";
import {
  WORK_KINDS,
  type WorkExpense,
  type WorkHeldBy,
  type WorkHour,
  type WorkMilestone,
  type WorkProject,
  type WorkScope,
  workQueryOptions,
} from "../api/work";
import { invoicesCatalog } from "../i18n";
import {
  blockerWords,
  consumptionRefusalMessage,
  refusalCode,
  warningMessage,
  workRefusalMessage,
} from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { type ChosenWork, totalsOf } from "../lib/work";
import { FromConsumptionWizard } from "../pages/-from-consumption-wizard";
import { FromWorkWizard } from "../pages/-from-work-wizard";
import { DocumentLink } from "./document-link";

export interface UninvoicedWorkPanelProps {
  /** The customer whose projects' work is listed — the customer page's Invoices tab. */
  customerId?: number;
  /** The one project whose work is listed — the project page's Invoicing tab. Exactly one of the two. */
  projectId?: number;
  /**
   * Whether the work may be made into a draft here. The host passes false on a
   * customer the server would take no draft for — not active, merged away or
   * anonymised — as it does for "New invoice": the work is listed, and the
   * wizard is not offered. True when left out.
   */
  canInvoice?: boolean;
}

/** A row of the view, whatever its kind, as the panel lists and chooses it. */
interface WorkRow {
  key: string;
  selectable: boolean;
  reason?: string;
  heldBy?: WorkHeldBy;
  warnings: string[];
  chosen: ChosenWork;
  /** The cells after the checkbox, in the kind's own columns. */
  cells: ReactNode[];
  /** What the row's checkbox is named after. */
  label: string;
}

const rowKey = (kind: string, id: number) => `${kind}:${id}`;

/**
 * The work not yet invoiced (invoices work design D3, D18), per project and
 * per kind — hours, expenses, milestones — each row with a checkbox. A row
 * that cannot be chosen is greyed with its reason: a fixed-price project's
 * hours (shown as information), a non-billable project, another currency than
 * NOK, or "On draft n" / "On invoice n", a link to the document that holds it.
 * The view's totals per currency are the selectable work's; the selection's
 * own totals sit beside "Invoice the chosen work", which opens the wizard. The
 * view's warnings — work overdue to invoice, a supplier invoice billed twice,
 * a list cut short — are said where they belong. The host mounts it for a
 * caller who holds `invoices:create`; an installation that invoices no work
 * shows nothing on a customer and says why on a project.
 *
 * On a customer the Consumption section follows (energy consumption billing
 * design D11): the customer's metering points with billing periods due, and
 * "From consumption", which opens that wizard. An installation with Energy
 * switched off shows no such section.
 */
export const UninvoicedWorkPanel = (props: UninvoicedWorkPanelProps) => (
  <>
    <UninvoicedWork {...props} />
    {props.customerId !== undefined && (
      <UninvoicedConsumption customerId={props.customerId} canInvoice={props.canInvoice ?? true} />
    )}
  </>
);

const UninvoicedWork = ({ customerId, projectId, canInvoice = true }: UninvoicedWorkPanelProps) => {
  const { t, money, unitPrice, number, date } = useInvoiceFormat();
  const scope: WorkScope = customerId !== undefined ? { customerId } : { projectId: projectId ?? 0 };
  const meta = useQuery(invoicesMetaQueryOptions());
  const available = meta.data?.workAvailable === true;
  const work = useQuery({ ...workQueryOptions(scope), enabled: available });
  const [chosenKeys, setChosenKeys] = useState<Set<string>>(new Set());
  const [invoicing, setInvoicing] = useState(false);

  if (meta.data && !available) {
    if (customerId !== undefined) return null;
    return (
      <Card withBorder padding="lg" radius="md" mt="md">
        <Text size="sm" c="dimmed">
          {t("refusal.work_unavailable")}
        </Text>
      </Card>
    );
  }

  const users = new Map((work.data?.users ?? []).map((u) => [u.id, u.displayName]));
  const projects = (work.data?.projects ?? []).map((project) => ({ project, groups: groupsOf(project) }));
  const rows = projects.flatMap(({ groups }) => groups.flatMap((g) => g.rows));
  // What is chosen and still on the view: a refetch after the wizard takes
  // held work out of the choice without the person unticking it.
  const chosen = rows.filter((r) => r.selectable && chosenKeys.has(r.key)).map((r) => r.chosen);
  const toggle = (keys: string[], on: boolean) =>
    setChosenKeys((current) => {
      const next = new Set(current);
      for (const key of keys) {
        if (on) next.add(key);
        else next.delete(key);
      }
      return next;
    });
  const amounts = (totals: { currency: string; amount: number }[]) =>
    totals.map((total) => money(total.amount, total.currency)).join(", ");

  function groupsOf(project: WorkProject) {
    const hours: WorkRow[] = project.hours.map((h: WorkHour) => {
      const person = users.get(h.userId) ?? t("unknownPerson");
      return {
        key: rowKey(WORK_KINDS.hours, h.id),
        selectable: h.selectable,
        reason: h.reason,
        heldBy: h.heldBy,
        warnings: h.warnings ?? [],
        chosen: {
          kind: WORK_KINDS.hours,
          id: h.id,
          revision: h.revision,
          projectId: project.id,
          date: h.date,
          amount: h.amount,
          currency: h.currency,
          rate: h.rate,
          userId: h.userId,
          workTypeId: h.workTypeId,
          label: t("workHourLabel", { person, date: date(h.date) }),
        },
        label: t("workHourLabel", { person, date: date(h.date) }),
        cells: [
          date(h.date),
          person,
          h.workTypeName ?? h.taskTitle ?? "",
          t("hoursAtRate", { hours: number(h.hours, 2), rate: unitPrice(h.rate, h.currency) }),
          money(h.amount, h.currency),
        ],
      };
    });
    const expenses: WorkRow[] = project.expenses.map((e: WorkExpense) => ({
      key: rowKey(WORK_KINDS.expenses, e.id),
      selectable: e.selectable,
      reason: e.reason,
      heldBy: e.heldBy,
      warnings: e.warnings ?? [],
      chosen: {
        kind: WORK_KINDS.expenses,
        id: e.id,
        revision: e.revision,
        projectId: project.id,
        date: e.date,
        amount: e.billAmount,
        currency: e.currency,
        expenseKind: e.kind,
        label: t("workExpenseLabel", { description: e.description, date: date(e.date) }),
      },
      label: t("workExpenseLabel", { description: e.description, date: date(e.date) }),
      cells: [
        date(e.date),
        `expenseKind.${e.kind}` in invoicesCatalog.en ? t(`expenseKind.${e.kind}`) : e.kind,
        e.kind === "supplier_invoice"
          ? t("supplierInvoiceOf", {
              supplier: e.supplier ?? "",
              number: e.supplierInvoiceNumber ?? "",
              description: e.description,
            })
          : e.description,
        e.kind === "mileage" && e.distanceKm !== undefined ? t("distanceKm", { km: e.distanceKm }) : "",
        money(e.billAmount, e.currency),
      ],
    }));
    const milestones: WorkRow[] = project.milestones.map((m: WorkMilestone) => ({
      key: rowKey(WORK_KINDS.milestones, m.id),
      selectable: m.selectable,
      reason: m.reason,
      heldBy: m.heldBy,
      warnings: m.warnings ?? [],
      chosen: {
        kind: WORK_KINDS.milestones,
        id: m.id,
        revision: m.revision,
        projectId: project.id,
        date: m.date,
        amount: m.amount,
        currency: m.currency,
        label: m.name,
      },
      label: m.name,
      cells: [date(m.date), m.name, m.description, "", money(m.amount, m.currency)],
    }));
    return [
      { kind: "hours", title: t("workHours"), rows: hours },
      { kind: "expenses", title: t("workExpenses"), rows: expenses },
      { kind: "milestones", title: t("workMilestones"), rows: milestones },
    ].filter((g) => g.rows.length > 0);
  }

  const columns: Record<string, string[]> = {
    hours: [t("workDate"), t("workPerson"), t("workType"), t("workHours"), t("workAmount")],
    expenses: [t("workDate"), t("kind"), t("description"), t("workDistance"), t("workAmount")],
    milestones: [t("workDate"), t("workMilestone"), t("description"), "", t("workAmount")],
  };

  return (
    <Card withBorder padding="lg" radius="md" mt="md" data-testid="uninvoiced-work">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap" align="flex-start">
          <Stack gap={2}>
            <Text fw={600} component="h3">
              {t("uninvoicedWork")}
            </Text>
            <Text size="sm" c="dimmed">
              {t("uninvoicedWorkDescription")}
            </Text>
          </Stack>
          {work.data?.customerId !== undefined && canInvoice && (
            <Button
              size="xs"
              leftSection={<IconFileInvoice size={14} />}
              disabled={chosen.length === 0}
              onClick={() => setInvoicing(true)}
            >
              {t("invoiceSelection")}
            </Button>
          )}
        </Group>
        {(meta.isError || work.isError) && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadWork")}>
            {workRefusalMessage(meta.error ?? work.error, t, date)}
          </Alert>
        )}
        {(meta.isPending || (available && work.isPending)) && <ContentSkeleton rows={3} rowHeight={40} />}
        {work.data && (
          <>
            {work.data.warnings.map((w) => (
              <Alert key={w} color="yellow" icon={<IconAlertCircle size={16} />} data-work-warning={w}>
                {warningMessage(w, t)}
              </Alert>
            ))}
            {work.data.customerId === undefined && (
              <Text size="sm" c="orange">
                {t("projectBillsNoCustomer")}
              </Text>
            )}
            {rows.length === 0 ? (
              <EmptyState title={t("noUninvoicedWork")} description={t("noUninvoicedWorkDescription")} size="sm" />
            ) : (
              <Group gap="lg" wrap="wrap">
                <Text size="sm" data-testid="work-totals">
                  {t("workTotals", { amounts: amounts(work.data.totals) || money(0, meta.data?.currency ?? "NOK") })}
                </Text>
                {chosen.length > 0 && (
                  <Text size="sm" fw={600} data-testid="work-chosen">
                    {t("workSelected", { count: chosen.length, amounts: amounts(totalsOf(chosen)) })}
                  </Text>
                )}
              </Group>
            )}
            {projects.map(({ project, groups }) =>
              groups.length === 0 ? null : (
                <Stack key={project.id} gap="xs" data-testid={`work-project-${project.id}`}>
                  <Group gap="xs">
                    <Title order={5}>{t("projectHeading", { code: project.code, name: project.name })}</Title>
                    {project.billingType !== "time-and-materials" && (
                      <Badge variant="light" color="gray">
                        {`billingType.${project.billingType}` in invoicesCatalog.en
                          ? t(`billingType.${project.billingType}`)
                          : project.billingType}
                      </Badge>
                    )}
                  </Group>
                  {project.warnings.map((w) => (
                    <Text key={w} size="sm" c="orange" data-work-warning={w}>
                      {warningMessage(w, t)}
                    </Text>
                  ))}
                  {groups.map((group) => {
                    const selectable = group.rows.filter((r) => r.selectable);
                    const all = selectable.length > 0 && selectable.every((r) => chosenKeys.has(r.key));
                    const some = selectable.some((r) => chosenKeys.has(r.key));
                    return (
                      <Table.ScrollContainer key={group.kind} minWidth={640}>
                        <Table aria-label={t("workTableOf", { kind: group.title, project: project.code })}>
                          <Table.Thead>
                            <Table.Tr>
                              <Table.Th w={40}>
                                <Checkbox
                                  aria-label={t("chooseAllOf", { kind: group.title, project: project.code })}
                                  disabled={selectable.length === 0}
                                  checked={all}
                                  indeterminate={some && !all}
                                  onChange={(e) =>
                                    toggle(
                                      selectable.map((r) => r.key),
                                      e.currentTarget.checked,
                                    )
                                  }
                                />
                              </Table.Th>
                              {columns[group.kind].map((c, i) => (
                                <Table.Th key={`${group.kind}-${i}`} ta={i === 4 ? "right" : undefined}>
                                  {c}
                                </Table.Th>
                              ))}
                              <Table.Th>{t("workStatus")}</Table.Th>
                            </Table.Tr>
                          </Table.Thead>
                          <Table.Tbody>
                            {group.rows.map((row) => (
                              <Table.Tr
                                key={row.key}
                                data-work-row={row.key}
                                data-selectable={row.selectable}
                                style={row.selectable ? undefined : { opacity: 0.6 }}
                              >
                                <Table.Td>
                                  <Checkbox
                                    aria-label={t("chooseRow", { label: row.label })}
                                    disabled={!row.selectable}
                                    checked={row.selectable && chosenKeys.has(row.key)}
                                    onChange={(e) => toggle([row.key], e.currentTarget.checked)}
                                  />
                                </Table.Td>
                                {row.cells.map((cell, i) => (
                                  <Table.Td key={`${row.key}-${i}`} ta={i === 4 ? "right" : undefined}>
                                    <Text size="sm" c={row.selectable ? undefined : "dimmed"}>
                                      {cell}
                                    </Text>
                                  </Table.Td>
                                ))}
                                <Table.Td>
                                  <Stack gap={2}>
                                    {!row.selectable && <Reason reason={row.reason} heldBy={row.heldBy} />}
                                    {row.warnings.map((w) => (
                                      <Text key={w} size="xs" c="orange" data-work-warning={w}>
                                        {warningMessage(w, t)}
                                      </Text>
                                    ))}
                                  </Stack>
                                </Table.Td>
                              </Table.Tr>
                            ))}
                          </Table.Tbody>
                        </Table>
                      </Table.ScrollContainer>
                    );
                  })}
                </Stack>
              ),
            )}
          </>
        )}
      </Stack>
      {invoicing && work.data?.customerId !== undefined && (
        <FromWorkWizard customerId={work.data.customerId} work={chosen} onClose={() => setInvoicing(false)} />
      )}
    </Card>
  );
};

/** Why a row cannot be chosen; held work names the document holding it, as a link. */
const Reason = ({ reason, heldBy }: { reason?: string; heldBy?: WorkHeldBy }) => {
  const { t } = useInvoiceFormat();
  if (reason === "held" && heldBy) {
    return (
      <Text size="xs">
        <DocumentLink invoiceId={heldBy.invoiceId}>
          {heldBy.number ? t("heldOnInvoice", { number: heldBy.number }) : t("heldOnDraft", { id: heldBy.invoiceId })}
        </DocumentLink>
      </Text>
    );
  }
  if (!reason) return null;
  return (
    <Text size="xs" c="dimmed">
      {`workReason.${reason}` in invoicesCatalog.en ? t(`workReason.${reason}`) : reason}
    </Text>
  );
};

/** A billing cadence in words: monthly, quarterly or yearly, or the code itself. */
const cadenceWords = (cadence: string, t: (key: string) => string): string =>
  `cadence.${cadence}` in invoicesCatalog.en ? t(`cadence.${cadence}`) : cadence;

/**
 * The customer's consumption due (energy consumption billing design D11): per
 * supply period the metering point's GSRN and address, the cadence, the day it
 * is billed through, how many billing periods are due and, when the first one
 * cannot be billed, why. "From consumption" opens the wizard, which settles
 * what is due and lists it. Energy switched off (409 `consumption_unavailable`)
 * shows nothing; nothing due says so.
 */
const UninvoicedConsumption = ({ customerId, canInvoice }: { customerId: number; canInvoice: boolean }) => {
  const { t, date } = useInvoiceFormat();
  const consumption = useQuery({
    ...consumptionQueryOptions(customerId),
    // A coded refusal is the module's answer, not a hiccup: it is not asked again.
    retry: (count, error) => refusalCode(error) === undefined && count < 2,
  });
  const [invoicing, setInvoicing] = useState(false);

  if (consumption.isPending) return null;
  if (refusalCode(consumption.error) === "consumption_unavailable") return null;
  const supplies: DueSupply[] = consumption.data?.supplies ?? [];

  return (
    <Card withBorder padding="lg" radius="md" mt="md" data-testid="uninvoiced-consumption">
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap" align="flex-start">
          <Stack gap={2}>
            <Text fw={600} component="h3">
              {t("consumption")}
            </Text>
            <Text size="sm" c="dimmed">
              {t("consumptionDescription")}
            </Text>
          </Stack>
          {consumption.data && canInvoice && (
            <Button size="xs" leftSection={<IconFileInvoice size={14} />} onClick={() => setInvoicing(true)}>
              {t("fromConsumption")}
            </Button>
          )}
        </Group>
        {consumption.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadConsumption")}>
            {consumptionRefusalMessage(consumption.error, t, date)}
          </Alert>
        )}
        {consumption.data?.warnings.map((w) => (
          <Alert key={w} color="yellow" icon={<IconAlertCircle size={16} />} data-consumption-warning={w}>
            {warningMessage(w, t)}
          </Alert>
        ))}
        {consumption.data &&
          (supplies.length === 0 ? (
            <EmptyState title={t("noConsumptionDue")} description={t("noConsumptionDueDescription")} size="sm" />
          ) : (
            <Table.ScrollContainer minWidth={640}>
              <Table aria-label={t("consumptionTable")}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("meteringPoint")}</Table.Th>
                    <Table.Th>{t("consumptionAddress")}</Table.Th>
                    <Table.Th>{t("billingCadence")}</Table.Th>
                    <Table.Th>{t("billedThrough")}</Table.Th>
                    <Table.Th ta="right">{t("duePeriods")}</Table.Th>
                    <Table.Th>{t("consumptionStatus")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {supplies.map((s) => (
                    <Table.Tr key={s.supplyPeriodId} data-supply={s.supplyPeriodId}>
                      <Table.Td>
                        <Text size="sm">{s.gsrn}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{s.address}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{cadenceWords(s.cadence, t)}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c={s.billedThrough ? undefined : "dimmed"}>
                          {s.billedThrough ? date(s.billedThrough) : t("notBilledYet")}
                        </Text>
                      </Table.Td>
                      <Table.Td ta="right">
                        <Text size="sm">{s.duePeriods}</Text>
                      </Table.Td>
                      <Table.Td>
                        {s.blocked ? (
                          <Text size="xs" c="orange" data-blocked={s.blocked}>
                            {blockerWords(s.blocked, t)}
                          </Text>
                        ) : (
                          <Text size="xs" c="dimmed">
                            {t("consumptionReady")}
                          </Text>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          ))}
      </Stack>
      {invoicing && <FromConsumptionWizard customerId={customerId} onClose={() => setInvoicing(false)} />}
    </Card>
  );
};

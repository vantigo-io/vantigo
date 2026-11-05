import { Alert, Button, Checkbox, Group, Modal, SegmentedControl, Stack, TextInput } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { type BillingRunInput, postBillingRun } from "../api/billing-runs";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import { RouteLink } from "../components/document-link";
import "../i18n";
import { fieldRefusals, refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { billingRunHref, billingRunLinkOptions } from "../lib/routes";

export interface NewBillingRunModalProps {
  /** Today in the business time zone (meta's), the through date's default. */
  today: string;
  /** Whether the caller may issue (meta's `canIssue`): only then is "Issue the invoices" offered. */
  canIssue: boolean;
  /** Whether the Orders module is on: only then may a run invoice orders (orders design O40). */
  ordersEnabled?: boolean;
  onClose: () => void;
}

const customerInputs = /^(through|combine|customerIds(\[\d+\])?)$/;

/**
 * A new billing run (energy consumption billing design D10, plan reading 11):
 * the through date, whether the invoices are issued — offered only to a caller
 * who may issue — and, optionally, the customers by id; absent, every customer
 * with consumption due. With Orders on, the run may invoice orders instead
 * (orders design O40): every order line due through the date, one invoice per
 * order or — combined — per customer where the orders agree. On 201 the run's
 * page; a refusal in words, with a link to the run in progress when one is.
 */
export const NewBillingRunModal = ({ today, canIssue, ordersEnabled = false, onClose }: NewBillingRunModalProps) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [through, setThrough] = useState(today);
  const [issue, setIssue] = useState(false);
  const [source, setSource] = useState<"consumption" | "orders">("consumption");
  const [combine, setCombine] = useState(false);
  const [ids, setIds] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refusal, setRefusal] = useState<{ words: string[]; runId?: number } | null>(null);

  const create = useMutation({
    mutationFn: (input: BillingRunInput) => postBillingRun(input),
    onMutate: () => {
      setErrors({});
      setRefusal(null);
    },
    onSuccess: async (run) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("billingRun.started") });
      onClose();
      navigate(billingRunLinkOptions(run.id));
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(error, t, (field) => customerInputs.test(field), "billingRun");
        const onIds = Object.entries(onInputs).find(([field]) => field.startsWith("customerIds"))?.[1];
        setErrors({
          ...(onInputs.through ? { through: onInputs.through } : {}),
          ...(onInputs.combine ? { combine: onInputs.combine } : {}),
          ...(onIds ? { customerIds: onIds } : {}),
        });
        if (elsewhere.length > 0) setRefusal({ words: elsewhere });
        return;
      }
      const runId = refusalProblem(error).runId;
      setRefusal({
        words: [refusalMessage(error, t, date, String, dateTime)],
        runId: refusalCode(error) === "billing_run_active" && typeof runId === "number" ? runId : undefined,
      });
    },
  });

  const submit = () => {
    const tokens = ids.split(/[\s,]+/).filter(Boolean);
    if (tokens.some((token) => !/^\d+$/.test(token))) {
      setErrors({ customerIds: t("billingRun.customerIdsInvalid") });
      return;
    }
    create.mutate({
      issue: canIssue && issue,
      ...(source === "orders" ? { source, combine } : {}),
      ...(through ? { through } : {}),
      ...(tokens.length > 0 ? { customerIds: tokens.map(Number) } : {}),
    });
  };

  return (
    <Modal opened onClose={onClose} title={t("billingRun.new")}>
      <Stack>
        {ordersEnabled && (
          <SegmentedControl
            aria-label={t("billingRun.source")}
            value={source}
            onChange={(v) => setSource(v as "consumption" | "orders")}
            data={[
              { value: "consumption", label: t("billingRun.source.consumption") },
              { value: "orders", label: t("billingRun.source.orders") },
            ]}
          />
        )}
        <DateInput
          label={source === "orders" ? t("billingRun.ordersThrough") : t("billingRun.through")}
          description={source === "orders" ? t("billingRun.ordersThroughHint") : t("billingRun.throughHint")}
          valueFormat={t("dateInputFormat")}
          value={through || null}
          error={errors.through}
          onChange={(day) => {
            if (day) setThrough(day);
          }}
        />
        {source === "orders" && (
          <Checkbox
            label={t("billingRun.combine")}
            description={t("billingRun.combineHint")}
            checked={combine}
            error={errors.combine}
            onChange={(event) => setCombine(event.currentTarget.checked)}
          />
        )}
        {canIssue && (
          <Checkbox
            label={t("billingRun.issue")}
            description={t("billingRun.issueHint")}
            checked={issue}
            onChange={(event) => setIssue(event.currentTarget.checked)}
          />
        )}
        <TextInput
          label={t("billingRun.customerIds")}
          description={t("billingRun.customerIdsHint")}
          value={ids}
          error={errors.customerIds}
          onChange={(event) => setIds(event.currentTarget.value)}
        />
        {refusal && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("billingRun.couldNotStart")}>
            <Stack gap={4}>
              {refusal.words.map((words) => (
                <span key={words}>{words}</span>
              ))}
              {refusal.runId !== undefined && (
                <RouteLink href={billingRunHref(refusal.runId)} to={billingRunLinkOptions(refusal.runId)}>
                  {t("billingRun.openActive")}
                </RouteLink>
              )}
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button loading={create.isPending} onClick={submit}>
            {t("billingRun.start")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

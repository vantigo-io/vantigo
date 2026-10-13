import { Alert, Anchor, Button, Center, Loader, Modal, Stack, Text } from "@mantine/core";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { useEffect, useEffectEvent, useRef, useState } from "react";
import {
  cancelPaymentRequest,
  createPaymentRequest,
  paymentRequestQueryOptions,
  readPaymentRequest,
  recoveryOf,
} from "../api/attempts";
import type { InvoiceDocument } from "../api/invoices";
import "../i18n";
import { refusalCode, refusalMessage, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";
import { QrScreen } from "./-qr-screen";

export interface TakePaymentModalProps {
  invoice: InvoiceDocument;
  onClose: () => void;
}

/** Where the modal stands: asking, showing a request's QR or state, or saying why there is none. */
type Step =
  | { kind: "asking" }
  | { kind: "qr"; attemptId: number; qrSvg?: string; notice?: string }
  | {
      kind: "refused";
      words: string;
      title?: string;
      customerLink?: boolean;
      staleAttemptId?: number;
      offerNew?: boolean;
    };

/**
 * "Take a Vipps payment now" (invoices payments and reminders design D17,
 * D22): asks for an on-site request of the invoice's whole open amount at
 * once and shows its QR code full screen, polled until it is over. Every
 * refusal is said in words; `buyer_identity_missing` links to the customer,
 * whose details the invoice needs. On `payment_request_active` the request
 * named by `attemptId` is read and recovered by `recoveryOf`, the rule the
 * quick invoice uses too: its QR code shown again while it is `created`; an
 * approved one followed with no cancel, the money perhaps on its way; one
 * without a code, still being created or unreadable offered to be cancelled
 * and made again — and one that cannot be cancelled left to a manager to
 * abandon; one that is over offered afresh. Nothing is set once the modal
 * is closed.
 */
export const TakePaymentModal = ({ invoice, onClose }: TakePaymentModalProps) => {
  const { t, date } = useInvoiceFormat();
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const [step, setStepState] = useState<Step>({ kind: "asking" });
  const started = useRef(false);
  // Whether the modal is still open, for the request's async steps.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const setStep = (next: Step) => {
    if (alive.current) setStepState(next);
  };

  const refused = (error: unknown): Step => ({
    kind: "refused",
    words: refusalMessage(error, t, date),
    customerLink: refusalCode(error) === "buyer_identity_missing",
  });

  /** The request a 409 named, read and recovered — never guessed. */
  const recover = async (attemptId: number) => {
    const open = await readPaymentRequest(invoice.id, attemptId).catch(() => undefined);
    const recovery = recoveryOf(open);
    if (recovery === "reshow") {
      setStep({ kind: "qr", attemptId, qrSvg: open?.qrSvg, notice: t("takePayment.activeShownAgain") });
    } else if (recovery === "follow") {
      // Seeded, so the QR screen starts on the state read and never offers a cancel meanwhile.
      queryClient.setQueryData(paymentRequestQueryOptions(invoice.id, attemptId).queryKey, open);
      setStep({ kind: "qr", attemptId });
    } else if (recovery === "cancel") {
      setStep({ kind: "refused", words: t("takePayment.activeNotShowable"), staleAttemptId: attemptId });
    } else {
      setStep({ kind: "refused", title: t("quick.paymentNotOpen"), words: "", offerNew: true });
    }
  };

  const ask = async () => {
    setStep({ kind: "asking" });
    try {
      const made = await createPaymentRequest(invoice.id);
      setStep({ kind: "qr", attemptId: made.attemptId, qrSvg: made.qrSvg });
    } catch (error) {
      const attemptId = refusalProblem(error).attemptId;
      if (refusalCode(error) === "payment_request_active" && typeof attemptId === "number") {
        if (alive.current) await recover(attemptId);
        return;
      }
      setStep(refused(error));
    }
  };

  const cancelAndRetry = async (attemptId: number) => {
    setStep({ kind: "asking" });
    try {
      await cancelPaymentRequest(invoice.id, attemptId);
    } catch (error) {
      setStep(
        refusalCode(error) === "payment_request_active"
          ? { kind: "refused", words: t("takePayment.parked") }
          : refused(error),
      );
      return;
    }
    if (alive.current) await ask();
  };

  // The request is asked for once, when the modal opens.
  const start = useEffectEvent(() => void ask());
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    start();
  }, []);

  const customerHref = `/customers/${invoice.customerId}`;
  return (
    <Modal opened onClose={onClose} title={t("takePayment.title")} fullScreen>
      {step.kind === "asking" && (
        <Stack align="center" py="xl">
          <Loader />
          <Text>{t("takePayment.asking")}</Text>
        </Stack>
      )}
      {step.kind === "qr" && (
        <QrScreen
          invoiceId={invoice.id}
          attemptId={step.attemptId}
          qrSvg={step.qrSvg}
          notice={step.notice}
          onClose={onClose}
        />
      )}
      {step.kind === "refused" && (
        <Center py="xl">
          <Stack maw={560}>
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={step.title ?? t("takePayment.couldNot")}>
              <Stack gap="xs">
                {step.words && <Text size="sm">{step.words}</Text>}
                {step.customerLink && (
                  <Anchor
                    size="sm"
                    href={appUrl(customerHref)}
                    onClick={(event) => {
                      if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
                      event.preventDefault();
                      navigate({ to: customerHref });
                    }}
                  >
                    {t("takePayment.openCustomer")}
                  </Anchor>
                )}
              </Stack>
            </Alert>
            {step.staleAttemptId !== undefined && (
              <Button onClick={() => void cancelAndRetry(step.staleAttemptId as number)}>
                {t("takePayment.cancelAndRetry")}
              </Button>
            )}
            {step.offerNew && <Button onClick={() => void ask()}>{t("takePayment.button")}</Button>}
            <Button variant="default" onClick={onClose}>
              {t("qr.close")}
            </Button>
          </Stack>
        </Center>
      )}
    </Modal>
  );
};

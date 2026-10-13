import {
  Alert,
  Anchor,
  Button,
  Card,
  Center,
  Checkbox,
  Group,
  Loader,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { IconAlertCircle, IconCircleCheck, IconClock } from "@tabler/icons-react";
import { type Query, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  type PayAttemptStatus,
  type PayPageView,
  payNavigation,
  readPayPage,
  readPayStatus,
  startPayment,
} from "../api/pay";
import { INVOICES_QUERY_KEY } from "../api/request";
import { invoicesCatalog } from "../i18n";
import { refusalCode, refusalProblem } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

/** Where the tab keeps the pay link's token, and a payment's reference and nonce on return. */
export const PAY_STORAGE_KEY = "vantigo.invoices.pay";

/**
 * The return page's poll (D15, the status bucket's 400 per 10 minutes): every
 * 2 s for the first minute, then every 5 s, and no more after ten minutes —
 * about 140 reads in all.
 */
export const PAY_POLL_FAST_MS = 2000;
export const PAY_POLL_FAST_READS = 30;
export const PAY_POLL_SLOW_MS = 5000;
/** Errors in a row double the delay, up to a minute; a 429 stops the poll until the payer asks again. */
export const PAY_POLL_ERROR_CAP_MS = 60_000;
export const PAY_POLL_MAX_READS =
  PAY_POLL_FAST_READS + (10 * 60_000 - PAY_POLL_FAST_READS * PAY_POLL_FAST_MS) / PAY_POLL_SLOW_MS;

interface PayAddress {
  token?: string;
  attempt?: string;
  nonce?: string;
}

const ADDRESS_PARAMS = ["token", "attempt", "nonce"] as const;

const storage = (): Storage | undefined => {
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
};

const remembered = (): PayAddress => {
  try {
    const raw = storage()?.getItem(PAY_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    return parsed && typeof parsed === "object" ? (parsed as PayAddress) : {};
  } catch {
    return {};
  }
};

const remember = (address: PayAddress) => {
  try {
    storage()?.setItem(PAY_STORAGE_KEY, JSON.stringify(address));
  } catch {
    // The page works without storage: the address is held in the page's state.
  }
};

/**
 * What the page's address names, read once: a pay link's token, or — on
 * return from Vipps — a payment's reference and nonce, with the token the tab
 * kept from before. Kept in the tab's storage, so a reload still knows it once
 * the address bar no longer does.
 */
const readAddress = (): PayAddress => {
  const params = new URLSearchParams(window.location.search);
  const token = params.get("token") || undefined;
  const attempt = params.get("attempt") || undefined;
  const nonce = params.get("nonce") || undefined;
  const kept = remembered();
  let address: PayAddress;
  if (token) address = attempt && nonce ? { token, attempt, nonce } : { token };
  else if (attempt && nonce) address = { token: kept.token, attempt, nonce };
  else address = kept;
  remember(address);
  return address;
};

/** Takes the token, the reference and the nonce out of the address bar, so history and screenshots never hold them. */
const clearAddressBar = () => {
  const url = new URL(window.location.href);
  if (!ADDRESS_PARAMS.some((name) => url.searchParams.has(name))) return;
  for (const name of ADDRESS_PARAMS) url.searchParams.delete(name);
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
};

const statusOf = (error: unknown) => (error as { status?: unknown } | null)?.status;

/** The words for a refusal, as a payer reads them: never the server's English, never the operator's detail. */
const refusalKey = (error: unknown): string => {
  if (statusOf(error) === 404) return "pay.refusal.notFound";
  const reason = refusalProblem(error).reason;
  if (typeof reason === "string" && `pay.reason.${reason}` in invoicesCatalog.en) return `pay.reason.${reason}`;
  switch (refusalCode(error)) {
    case "rate_limited":
      return "pay.refusal.rateLimited";
    case "too_many_attempts":
      return "pay.refusal.too_many_attempts";
    case "payments_unavailable":
      return "pay.reason.unavailable";
    case "provider_failed":
      return "pay.refusal.provider_failed";
  }
  return "pay.refusal.generic";
};

type StatusQuery = Query<PayAttemptStatus, Error, PayAttemptStatus, readonly unknown[]>;

const rateLimited = (error: unknown) => statusOf(error) === 429 || refusalCode(error) === "rate_limited";

/**
 * The next read's delay, or false once the payment is over, unknown, rate
 * limited, or ten minutes have passed; `errorsInARow` failed reads double it,
 * up to a minute.
 */
const nextPoll = (query: Pick<StatusQuery, "state">, errorsInARow: number): number | false => {
  const { data, error, dataUpdateCount, errorUpdateCount } = query.state;
  if (data && data.state !== "pending") return false;
  if (error && (statusOf(error) === 404 || rateLimited(error))) return false;
  const reads = dataUpdateCount + errorUpdateCount;
  if (reads >= PAY_POLL_MAX_READS) return false;
  const delay = reads < PAY_POLL_FAST_READS ? PAY_POLL_FAST_MS : PAY_POLL_SLOW_MS;
  return Math.min(PAY_POLL_ERROR_CAP_MS, delay * 2 ** errorsInARow);
};

const Fact = ({ label, children, testId }: { label: string; children: ReactNode; testId?: string }) => (
  <div>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text fw={500} data-testid={testId}>
      {children}
    </Text>
  </div>
);

/** The invoice as the payer sees it: the seller, the number, the dates, the amount open and the KID. */
const InvoiceFacts = ({ page }: { page: PayPageView }) => {
  const { t, money, date } = useInvoiceFormat();
  return (
    <Stack gap="sm">
      <Fact label={t("pay.seller")}>{page.seller.name}</Fact>
      <SimpleGrid cols={{ base: 1, xs: 2 }} spacing="sm">
        <Fact label={t("pay.organisationNumber")}>{page.seller.organisationNumber}</Fact>
        <Fact label={t("pay.invoiceNumber")}>{page.invoiceNumber}</Fact>
        <Fact label={t("pay.issueDate")}>{date(page.issueDate)}</Fact>
        {page.dueDate && (
          <Fact label={t("pay.dueDate")} testId="pay-due-date">
            {date(page.dueDate)}
          </Fact>
        )}
      </SimpleGrid>
      <Fact label={t("pay.openAmount")} testId="pay-open-amount">
        {money(page.openAmount, page.currency)}
      </Fact>
      {page.kid && (
        <div>
          <Fact label={t("pay.kid")}>{page.kid}</Fact>
          <Text size="sm" c="dimmed">
            {t("pay.kidHelp")}
          </Text>
        </div>
      )}
    </Stack>
  );
};

/** The terms, their acceptance and "Pay with Vipps", which leaves for Vipps at once. */
const PayForm = ({ token, page }: { token: string; page: PayPageView }) => {
  const { t } = useInvoiceFormat();
  const [accepted, setAccepted] = useState(false);
  const [mustAccept, setMustAccept] = useState(false);
  const pay = useMutation({
    mutationFn: () => startPayment(token),
    onSuccess: (created) => payNavigation.open(created.redirectUrl),
  });
  const submit = () => {
    if (!accepted) {
      setMustAccept(true);
      return;
    }
    pay.mutate();
  };
  return (
    <Stack gap="md">
      {(page.termsUrl || page.privacyUrl) && (
        <Group gap="md">
          {page.termsUrl && (
            <Anchor href={page.termsUrl} target="_blank" rel="noopener noreferrer">
              {t("pay.terms")}
            </Anchor>
          )}
          {page.privacyUrl && (
            <Anchor href={page.privacyUrl} target="_blank" rel="noopener noreferrer">
              {t("pay.privacy")}
            </Anchor>
          )}
        </Group>
      )}
      <Checkbox
        label={t("pay.accept")}
        checked={accepted}
        onChange={(event) => {
          setAccepted(event.currentTarget.checked);
          if (event.currentTarget.checked) setMustAccept(false);
        }}
        error={mustAccept ? t("pay.acceptRequired") : undefined}
      />
      {pay.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
          {t(refusalKey(pay.error))}
        </Alert>
      )}
      <Button size="lg" fullWidth loading={pay.isPending || pay.isSuccess} onClick={submit}>
        {t("pay.payWithVipps")}
      </Button>
    </Stack>
  );
};

/** The payment's state on return from Vipps, read with backoff until it is over. */
const ReturnState = ({
  attempt,
  nonce,
  canRetry,
  onRetry,
}: {
  attempt: string;
  nonce: string;
  canRetry: boolean;
  onRetry: () => void;
}) => {
  const { t } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const statusKey = [INVOICES_QUERY_KEY, "pay-status", attempt];
  const errorsInARow = useRef(0);
  const status = useQuery({
    queryKey: statusKey,
    queryFn: async ({ signal }) => {
      try {
        const answer = await readPayStatus(attempt, nonce, signal);
        errorsInARow.current = 0;
        return answer;
      } catch (error) {
        errorsInARow.current += 1;
        throw error;
      }
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) => nextPoll(query, errorsInARow.current),
    // A hidden tab does not poll; the poll goes on when the payer comes back.
    refetchIntervalInBackground: false,
  });
  const state = status.data?.state;
  useEffect(() => {
    // Paid: the amount open is read again.
    if (state === "paid") void queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "pay-page"] });
  }, [state, queryClient]);
  if (status.isError && statusOf(status.error) === 404)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
        {t("pay.state.notFound")}
      </Alert>
    );
  if (status.isError && rateLimited(status.error))
    return (
      <Stack gap="sm">
        <Alert color="orange" icon={<IconAlertCircle size={16} />} role="status">
          {t("pay.refusal.rateLimited")}
        </Alert>
        <Button variant="light" loading={status.isFetching} onClick={() => void status.refetch()}>
          {t("pay.checkAgain")}
        </Button>
      </Stack>
    );
  if (state === "paid")
    return (
      <Alert color="green" icon={<IconCircleCheck size={16} />} role="status">
        {t("pay.state.paid")}
      </Alert>
    );
  if (state === "failed" || state === "cancelled")
    return (
      <Stack gap="sm">
        <Alert color="orange" icon={<IconAlertCircle size={16} />} role="status">
          {t(`pay.state.${state}`)}
        </Alert>
        {canRetry && (
          <Button variant="light" onClick={onRetry}>
            {t("pay.tryAgain")}
          </Button>
        )}
      </Stack>
    );
  // Every read starts and ends a fetch, so the page renders — and counts the reads again — after each.
  const counted = status.isFetching ? undefined : queryClient.getQueryState(statusKey);
  const reads = counted ? counted.dataUpdateCount + counted.errorUpdateCount : 0;
  if (status.data && reads >= PAY_POLL_MAX_READS)
    return (
      <Stack gap="sm">
        <Alert color="blue" icon={<IconClock size={16} />} role="status">
          {t("pay.state.stillPending")}
        </Alert>
        <Button variant="light" loading={status.isFetching} onClick={() => void status.refetch()}>
          {t("pay.checkAgain")}
        </Button>
      </Stack>
    );
  return (
    <Stack gap="xs" align="center" role="status">
      <Loader size="sm" />
      <Text>{t("pay.state.pending")}</Text>
      {status.isError && (
        <Text size="sm" c="dimmed">
          {t("pay.state.couldNotRead")}
        </Text>
      )}
    </Stack>
  );
};

/**
 * The public pay page (invoices payments and reminders design D15, D22), at
 * `/pay` on the public host, without a session: the invoice a pay link names —
 * the seller, the number, the dates, the amount open and the KID to pay by bank
 * instead, never the buyer — the seller's terms to accept, and "Pay with
 * Vipps", which leaves for Vipps' address at once and unchanged. Vipps sends
 * the payer back to `/pay?attempt=&nonce=`, where the payment's state is read
 * with backoff. The token, the reference and the nonce are read from the
 * address once, kept in the tab's storage, and taken out of the address bar.
 * The page speaks the buyer's language, as the read answers it, until the
 * reader chooses the other.
 */
export interface PayPageProps {
  /**
   * Takes the token, the reference and the nonce out of the address bar once
   * they are read — the host's router, so its own location agrees. Without it
   * the page replaces the browser's address itself, keeping `history.state`.
   */
  clearAddress?: () => void;
}

export const PayPage = ({ clearAddress }: PayPageProps) => {
  const { t, locale, setLanguagePreference } = useI18n("invoices");
  const queryClient = useQueryClient();
  const [address, setAddress] = useState<PayAddress>(readAddress);
  const [chosen, setChosen] = useState<"nb" | "en" | undefined>();
  // Once, on the address the page was opened at.
  const clear = useRef(clearAddress ?? clearAddressBar);
  useEffect(() => clear.current(), []);
  const { token, attempt, nonce } = address;
  const read = useQuery({
    queryKey: [INVOICES_QUERY_KEY, "pay-page"],
    queryFn: () => readPayPage(token ?? ""),
    enabled: !!token,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const language = chosen ?? read.data?.language;
  useEffect(() => {
    if (language) setLanguagePreference(language);
  }, [language, setLanguagePreference]);
  const retry = () => {
    const next = { token };
    remember(next);
    setAddress(next);
    void queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "pay-page"] });
  };
  const returning = !!attempt && !!nonce;
  const page = read.data;
  return (
    <Center mih="100vh" p="md" bg="gray.0">
      <Stack w="100%" maw={440} gap="md" component="main">
        <Card withBorder shadow="sm" padding="lg" radius="md">
          <Stack gap="md">
            <Group justify="space-between" align="flex-start" wrap="wrap">
              <Title order={1} size="h2">
                {t("pay.title")}
              </Title>
              <SegmentedControl
                size="xs"
                aria-label={t("pay.language.label")}
                value={language ?? locale}
                onChange={(value) => setChosen(value as "nb" | "en")}
                data={[
                  { value: "nb", label: t("pay.language.nb") },
                  { value: "en", label: t("pay.language.en") },
                ]}
              />
            </Group>
            {returning && <ReturnState attempt={attempt} nonce={nonce} canRetry={!!token} onRetry={retry} />}
            {!token && !returning && <Text>{t("pay.noLink")}</Text>}
            {token && read.isPending && (
              <Center>
                <Loader size="sm" />
              </Center>
            )}
            {read.isError && (
              <Alert color="red" icon={<IconAlertCircle size={16} />} role="alert">
                {t(refusalKey(read.error))}
              </Alert>
            )}
            {page && <InvoiceFacts page={page} />}
            {page && !page.payable && page.reason && !returning && (
              <Alert color="orange" icon={<IconAlertCircle size={16} />} role="status">
                {t(`pay.reason.${page.reason}`)}
              </Alert>
            )}
            {page?.payable && token && !returning && <PayForm token={token} page={page} />}
          </Stack>
        </Card>
      </Stack>
    </Center>
  );
};

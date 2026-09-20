import { Input, NumberInput, SegmentedControl, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import "../i18n";
import { useDecimalSeparator, useExpenseFormat } from "../lib/format";
import { netOf, type VatChoice, vatFromGross, vatRates } from "../lib/money";

export interface VatFieldProps {
  /** The gross the VAT is taken out of; whatever stands in the amount input. */
  gross: number;
  value: number | string;
  onChange: (value: number | string) => void;
  /** The expense's own currency — the net is written in it, never converted. */
  currency: string;
  error?: ReactNode;
}

/**
 * The VAT an outlay carries, with a helper that works it out of the gross:
 * `gross × r / (100 + r)`, half-up to two places.
 *
 * The helper only ever *fills* the field. Whatever stands in the field is
 * what is sent, so a hand-typed figure wins — and typing one lets go of the
 * helper's choice, because the number no longer means "25 % of the gross".
 * The net is shown beside it because that, not the VAT, is what the project
 * is charged and what a markup is taken on.
 */
export const VatField = ({ gross, value, onChange, currency, error }: VatFieldProps) => {
  const { t } = useI18n("expenses");
  const format = useExpenseFormat();
  const decimalSeparator = useDecimalSeparator();
  const [choice, setChoice] = useState<VatChoice>("none");

  const entered = typeof value === "number" ? value : Number(value.replace(",", "."));
  const vat = Number.isFinite(entered) ? entered : 0;

  const pick = (next: string) => {
    if (next === "none") {
      setChoice("none");
      onChange("");
      return;
    }
    const rate = Number(next) as (typeof vatRates)[number];
    setChoice(rate);
    onChange(gross > 0 ? vatFromGross(gross, rate) : "");
  };

  return (
    <Stack gap={4}>
      <NumberInput
        label={t("vatAmount")}
        decimalScale={2}
        decimalSeparator={decimalSeparator}
        min={0}
        value={value}
        error={error}
        onChange={(next) => {
          // The person is typing: the figure is theirs now, not the helper's.
          setChoice("none");
          onChange(next);
        }}
      />
      <Input.Wrapper label={t("vatHelper")} labelElement="div" size="xs">
        <SegmentedControl
          size="xs"
          mt={4}
          fullWidth
          aria-label={t("vatHelper")}
          value={String(choice)}
          onChange={pick}
          data={[
            ...vatRates.map((rate) => ({ value: String(rate), label: t("vatPercent", { rate }) })),
            { value: "none", label: t("vatNone") },
          ]}
        />
      </Input.Wrapper>
      <Text size="xs" c="dimmed">
        {t("netIs", { amount: format.money(netOf(gross, vat), currency) })}
      </Text>
    </Stack>
  );
};

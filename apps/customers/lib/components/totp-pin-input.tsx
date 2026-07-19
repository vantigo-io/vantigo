"use client";

import { Center, PinInput, Text } from "@mantine/core";

/** Six-digit TOTP input with a centered error message. */
export function TotpPinInput({
  testId,
  value,
  onChange,
  onComplete,
  disabled,
  error,
}: Readonly<{
  testId: string;
  value: string;
  onChange: (value: string) => void;
  onComplete: (value: string) => void;
  disabled: boolean;
  error: string | null;
}>) {
  return (
    <>
      <Center>
        <PinInput
          length={6}
          type="number"
          oneTimeCode
          autoFocus
          data-testid={testId}
          value={value}
          onChange={onChange}
          onComplete={onComplete}
          disabled={disabled}
          error={error !== null}
        />
      </Center>
      {error && (
        <Text size="sm" c="red" ta="center">
          {error}
        </Text>
      )}
    </>
  );
}

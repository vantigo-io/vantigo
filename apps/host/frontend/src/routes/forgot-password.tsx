import { Alert, Button, Card, Center, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { requestPasswordRecovery } from "../api/account-lifecycle";

const ForgotPasswordPage = () => {
  const form = useForm({ initialValues: { email: "" } });
  const mutation = useMutation({ mutationFn: () => requestPasswordRecovery(form.values.email) });
  return (
    <Center mih="100vh" bg="gray.0">
      <Card withBorder p="xl" maw={440} w="100%">
        <Stack>
          <Title order={2}>Reset your password</Title>
          <Text c="dimmed">Enter your email and, if an account matches, we’ll send a reset link.</Text>
          {mutation.isSuccess && (
            <Alert color="teal">
              Check your inbox for next steps. This message is the same whether or not an account exists.
            </Alert>
          )}
          <form onSubmit={form.onSubmit(() => mutation.mutate())}>
            <Stack>
              <TextInput label="Email" required {...form.getInputProps("email")} />
              <Button type="submit" loading={mutation.isPending}>
                Send reset link
              </Button>
            </Stack>
          </form>
        </Stack>
      </Card>
    </Center>
  );
};
export const Route = createFileRoute("/forgot-password")({ component: ForgotPasswordPage });

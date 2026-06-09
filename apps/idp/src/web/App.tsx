import { MantineProvider } from "@mantine/core";
import { Button } from "@vantigo/ui";

export function App() {
  return (
    <MantineProvider>
      <div
        style={{
          padding: "4rem",
          textAlign: "center",
          fontFamily: "sans-serif",
        }}
      >
        <h1 style={{ fontSize: "3rem", marginBottom: "1rem" }}>Vantigo IDP</h1>
        <p style={{ color: "gray", marginBottom: "2rem" }}>
          Centralized Identity Provider powered by Edge Workers
        </p>
        <Button size="lg">Login with SSO</Button>
      </div>
    </MantineProvider>
  );
}

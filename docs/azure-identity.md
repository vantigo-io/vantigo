# Azure identity

Vantigo registers one process-wide `Azure.Core.TokenCredential` through the
`Vantigo.Azure.Identity` package. `AZURE__IDENTITY__ENABLED` defaults to `true`;
set it to `false` when the application must not expose an Azure credential.

When enabled, the credential is an Azure SDK `DefaultAzureCredential`. Vantigo
does not replace or reinterpret the standard Azure SDK environment variables,
including `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_SECRET`,
`AZURE_FEDERATED_TOKEN_FILE`, and `AZURE_AUTHORITY_HOST`. Other standard SDK
credential mechanisms remain available unchanged.

Creating the credential does not acquire a token or contact Azure during
startup. The first Azure client operation can still fail later if no usable
credential is available or the service is unreachable.

Azure Blob uses the global credential only with
`STORAGE__AUTHENTICATION=azure-identity`. If identity is disabled while that
mode is selected, startup/DI configuration fails with an actionable message;
choose `connection-string` or `sas` instead. Those modes do not resolve the
global credential.

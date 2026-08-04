using Microsoft.OpenApi;

namespace Vantigo.Communications.Api.Endpoints;

internal static class OpenApiMetadata
{
    internal static OpenApiOperation AddCreateHeaders(OpenApiOperation operation)
    {
        operation.Parameters ??= [];
        operation.Parameters.Add(new OpenApiParameter
        {
            Name = "X-Vantigo-Api-Key",
            In = ParameterLocation.Header,
            Required = false,
            Description = "Configured service API key. This is an opaque header credential, not a standard OpenAPI authentication scheme.",
            Schema = new OpenApiSchema { Type = JsonSchemaType.String },
        });
        operation.Parameters.Add(new OpenApiParameter
        {
            Name = "Idempotency-Key",
            In = ParameterLocation.Header,
            Required = true,
            Description = "Opaque client key required to make retries safe. Reusing it with a different payload returns 409 Conflict.",
            Schema = new OpenApiSchema { Type = JsonSchemaType.String, MaxLength = 200 },
        });
        return operation;
    }
}
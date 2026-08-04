using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace Vantigo.Communications.Api.Services;

public static class EmailPayloadFingerprint
{
    public static string Create(object payload)
    {
        var json = JsonSerializer.Serialize(payload, new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.CamelCase });
        return Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(json))).ToLowerInvariant();
    }
}
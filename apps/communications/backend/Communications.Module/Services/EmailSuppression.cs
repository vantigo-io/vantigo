namespace Vantigo.Communications.Services;

public static class EmailSuppression
{
    public static string Normalize(string address) => address.Trim().ToUpperInvariant();
}
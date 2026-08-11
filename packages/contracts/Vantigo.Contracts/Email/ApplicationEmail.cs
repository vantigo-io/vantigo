namespace Vantigo.Contracts.Email;

public sealed record ApplicationEmail(string To, string Subject, string TextBody);
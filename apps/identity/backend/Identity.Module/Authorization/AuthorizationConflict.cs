using Microsoft.EntityFrameworkCore;

using Npgsql;

namespace Vantigo.Identity.Authorization;

internal static class AuthorizationConflict
{
    internal static bool IsExpected(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is DbUpdateConcurrencyException) return true;
            if (current is PostgresException postgres && postgres.SqlState is
                PostgresErrorCodes.UniqueViolation or
                PostgresErrorCodes.SerializationFailure or
                PostgresErrorCodes.DeadlockDetected)
                return true;
        }

        return false;
    }
}
namespace Vantigo.Host;

public static class VantigoCommandDispatcher
{
    public static async Task<bool?> ExecuteDatabaseCommandAsync(
        VantigoCommand command,
        Func<Task> migrate,
        Func<Task<bool>> resetCommunications)
    {
        switch (command)
        {
            case VantigoCommand.Migrate:
                await migrate();
                return true;
            case VantigoCommand.ResetCommunications:
                return await resetCommunications();
            default:
                return null;
        }
    }
}
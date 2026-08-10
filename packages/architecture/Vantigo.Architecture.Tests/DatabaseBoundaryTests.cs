using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata;

using Vantigo.Communications.Database.Communications;
using Vantigo.Customers.Database.Customers;
using Vantigo.Products.Database.Products;

namespace Vantigo.Architecture.Tests;

public sealed class DatabaseBoundaryTests
{
    private static readonly IReadOnlyList<DatabaseModuleDefinition> Modules =
    [
        new(
            "Products",
            "products",
            "apps/products/backend/Products.Module/Database/Products",
            () => BuildModel<ProductsDbContext>(options => new ProductsDbContext(options))),
        new(
            "Customers",
            "customers",
            "apps/customers/backend/Customers.Module/Database/Customers",
            () => BuildModel<CustomersDbContext>(options => new CustomersDbContext(options))),
        new(
            "Communications",
            "communications",
            "apps/communications/backend/Communications.Module/Database/Communications",
            () => BuildModel<CommunicationsDbContext>(options => new CommunicationsDbContext(options))),
    ];

    public static IEnumerable<object[]> ModuleData =>
        Modules.Select(module => new object[] { module });

    [Theory]
    [MemberData(nameof(ModuleData))]
    public void Module_db_context_entities_are_mapped_to_the_module_schema(DatabaseModuleDefinition module)
    {
        var model = module.BuildModel();
        var defaultSchema = model.GetDefaultSchema();
        var violations = model.GetEntityTypes()
            .Select(entityType => new
            {
                Entity = entityType.DisplayName(),
                Schema = entityType.GetSchema() ?? defaultSchema,
            })
            .Where(mapping => !string.Equals(mapping.Schema, module.Schema, StringComparison.Ordinal))
            .Select(mapping => $"{mapping.Entity} -> {mapping.Schema ?? "<null>"}")
            .ToArray();

        Assert.True(
            violations.Length == 0,
            $"{module.Name} maps entities outside its owned schema '{module.Schema}': {string.Join(", ", violations)}");
    }

    [Theory]
    [MemberData(nameof(ModuleData))]
    public void Module_migrations_do_not_reference_another_module_schema_in_string_literals(
        DatabaseModuleDefinition module)
    {
        var repoRoot = FindRepositoryRoot();
        var migrationsDirectory = Path.Combine(repoRoot, module.DatabaseSourcePath, "Migrations");

        Assert.True(Directory.Exists(migrationsDirectory), $"Migration directory was not found: {migrationsDirectory}");

        var migrationFiles = Directory.EnumerateFiles(migrationsDirectory, "*.cs", SearchOption.AllDirectories)
            .OrderBy(path => path, StringComparer.Ordinal)
            .ToArray();

        Assert.NotEmpty(migrationFiles);

        var otherSchemas = Modules
            .Where(other => !ReferenceEquals(other, module))
            .Select(other => other.Schema)
            .ToArray();
        var violations = new List<string>();

        foreach (var migrationFile in migrationFiles)
        {
            var source = File.ReadAllText(migrationFile);
            foreach (var literal in ExtractStringLiterals(source))
            {
                foreach (var schema in otherSchemas)
                {
                    if (ContainsSchemaQualifier(literal.Value, schema))
                    {
                        violations.Add($"{Path.GetRelativePath(repoRoot, migrationFile)}:{literal.Line} contains '{schema}.'");
                    }
                }
            }
        }

        Assert.True(
            violations.Count == 0,
            $"{module.Name} migrations reference another module's schema: {string.Join(", ", violations)}");
    }

    private static IModel BuildModel<TContext>(Func<DbContextOptions<TContext>, TContext> createContext)
        where TContext : DbContext
    {
        var options = new DbContextOptionsBuilder<TContext>()
            .UseNpgsql("Host=localhost")
            .Options;

        using var context = createContext(options);
        return context.Model;
    }

    private static string FindRepositoryRoot()
    {
        for (var directory = new DirectoryInfo(AppContext.BaseDirectory);
             directory is not null;
             directory = directory.Parent)
        {
            if (File.Exists(Path.Combine(directory.FullName, "Vantigo.slnx")))
            {
                return directory.FullName;
            }
        }

        throw new DirectoryNotFoundException(
            $"Could not locate Vantigo.slnx above the test assembly directory '{AppContext.BaseDirectory}'.");
    }

    private static bool ContainsSchemaQualifier(string literal, string schema)
    {
        for (var index = literal.IndexOf(schema + ".", StringComparison.OrdinalIgnoreCase);
             index >= 0;
             index = literal.IndexOf(schema + ".", index + 1, StringComparison.OrdinalIgnoreCase))
        {
            if (index == 0 || !IsIdentifierCharacter(literal[index - 1]))
            {
                return true;
            }
        }

        return false;
    }

    private static bool IsIdentifierCharacter(char value) =>
        char.IsLetterOrDigit(value) || value is '_' or '$';

    private static IEnumerable<StringLiteral> ExtractStringLiterals(string source)
    {
        var index = 0;
        while (index < source.Length)
        {
            if (source[index] == '/' && index + 1 < source.Length && source[index + 1] == '/')
            {
                index = SkipLineComment(source, index + 2);
                continue;
            }

            if (source[index] == '/' && index + 1 < source.Length && source[index + 1] == '*')
            {
                index = SkipBlockComment(source, index + 2);
                continue;
            }

            if (source[index] == '\'')
            {
                index = SkipCharacterLiteral(source, index + 1);
                continue;
            }

            if (!TryGetStringStart(source, index, out var quoteIndex, out var isVerbatim))
            {
                index++;
                continue;
            }

            var line = GetLineNumber(source, index);
            var quoteLength = CountQuotes(source, quoteIndex);
            var contentStart = quoteIndex + quoteLength;

            if (quoteLength >= 3)
            {
                var closingQuote = source.IndexOf(new string('"', quoteLength), contentStart, StringComparison.Ordinal);
                if (closingQuote < 0)
                {
                    yield break;
                }

                yield return new StringLiteral(source[contentStart..closingQuote], line);
                index = closingQuote + quoteLength;
                continue;
            }

            var contentEnd = contentStart;
            while (contentEnd < source.Length)
            {
                if (!isVerbatim && source[contentEnd] == '\\')
                {
                    contentEnd += Math.Min(2, source.Length - contentEnd);
                    continue;
                }

                if (source[contentEnd] == '"')
                {
                    if (isVerbatim && contentEnd + 1 < source.Length && source[contentEnd + 1] == '"')
                    {
                        contentEnd += 2;
                        continue;
                    }

                    break;
                }

                contentEnd++;
            }

            if (contentEnd >= source.Length)
            {
                yield break;
            }

            yield return new StringLiteral(source[contentStart..contentEnd], line);
            index = contentEnd + 1;
        }
    }

    private static bool TryGetStringStart(string source, int index, out int quoteIndex, out bool isVerbatim)
    {
        quoteIndex = index;
        isVerbatim = false;

        if (source[index] == '"')
        {
            return true;
        }

        if (source[index] is not '$' and not '@')
        {
            return false;
        }

        var prefixEnd = index;
        while (prefixEnd < source.Length && source[prefixEnd] is '$' or '@')
        {
            isVerbatim |= source[prefixEnd] == '@';
            prefixEnd++;
        }

        if (prefixEnd >= source.Length || source[prefixEnd] != '"')
        {
            return false;
        }

        quoteIndex = prefixEnd;
        return true;
    }

    private static int CountQuotes(string source, int index)
    {
        var count = 0;
        while (index + count < source.Length && source[index + count] == '"')
        {
            count++;
        }

        return count;
    }

    private static int SkipLineComment(string source, int index)
    {
        while (index < source.Length && source[index] is not '\r' and not '\n')
        {
            index++;
        }

        return index;
    }

    private static int SkipBlockComment(string source, int index)
    {
        while (index + 1 < source.Length)
        {
            if (source[index] == '*' && source[index + 1] == '/')
            {
                return index + 2;
            }

            index++;
        }

        return source.Length;
    }

    private static int SkipCharacterLiteral(string source, int index)
    {
        while (index < source.Length)
        {
            if (source[index] == '\\')
            {
                index += Math.Min(2, source.Length - index);
                continue;
            }

            if (source[index] == '\'')
            {
                return index + 1;
            }

            index++;
        }

        return source.Length;
    }

    private static int GetLineNumber(string source, int index)
    {
        var line = 1;
        for (var current = 0; current < index; current++)
        {
            if (source[current] == '\n')
            {
                line++;
            }
        }

        return line;
    }

    public sealed record DatabaseModuleDefinition(
        string Name,
        string Schema,
        string DatabaseSourcePath,
        Func<IModel> BuildModel);

    private sealed record StringLiteral(string Value, int Line);
}
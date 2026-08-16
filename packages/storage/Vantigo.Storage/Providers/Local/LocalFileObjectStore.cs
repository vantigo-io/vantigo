using System.Runtime.InteropServices;

using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Validation;

namespace Vantigo.Storage.Providers.Local;

#pragma warning disable CA1416

/// <summary>Local filesystem implementation with atomic writes and root containment checks.</summary>
internal sealed class LocalFileObjectStore : IObjectStore, IStorageInitializer
{
    private readonly string _rootPath;
    private readonly Lazy<Task> _initialization;

    public LocalFileObjectStore(IOptions<StorageOptions> options, IHostEnvironment environment)
        : this(GetOptions(options.Value), environment)
    {
    }

    private LocalFileObjectStore(LocalStorageOptions options, IHostEnvironment environment)
    {
        options.Validate();
        ArgumentNullException.ThrowIfNull(environment);
        _rootPath = Path.GetFullPath(options.RootPath!);
        _allowInsecureRootForDevelopment = options.AllowInsecureRootForDevelopment;
        _environmentName = environment.EnvironmentName;
        _isDevelopment = environment.IsDevelopment();
        _initialization = new(InitializeCoreAsync, LazyThreadSafetyMode.ExecutionAndPublication);
    }

    public async Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default)
    {
        var path = await ResolvePathAsync(key, cancellationToken);
        ArgumentNullException.ThrowIfNull(content);
        ArgumentException.ThrowIfNullOrWhiteSpace(contentType);

        var parent = Path.GetDirectoryName(path)!;
        Directory.CreateDirectory(parent);
        EnsureNoSymlinkPath(parent);
        EnsureSecureDirectoryPath(parent);
        var temporaryPath = Path.Combine(parent, $".{Path.GetFileName(path)}.{Guid.NewGuid():N}.tmp");
        try
        {
            var fileOptions = new FileStreamOptions
            {
                Mode = FileMode.CreateNew,
                Access = FileAccess.Write,
                Share = FileShare.None,
                BufferSize = 64 * 1024,
                Options = FileOptions.Asynchronous | FileOptions.SequentialScan,
                UnixCreateMode = UnixFileMode.UserRead | UnixFileMode.UserWrite,
            };
            await using (var temporary = new FileStream(temporaryPath, fileOptions))
            {
                await content.CopyToAsync(temporary, cancellationToken);
                await temporary.FlushAsync(cancellationToken);
            }

            EnsureNoSymlinkPath(temporaryPath);
            EnsureNoSymlinkPath(path);
            File.Move(temporaryPath, path, overwrite: true);
            EnsureNoSymlinkPath(path);
            if (IsUnix())
                File.SetUnixFileMode(path, UnixFileMode.UserRead | UnixFileMode.UserWrite);
        }
        finally
        {
            try { if (File.Exists(temporaryPath)) File.Delete(temporaryPath); }
            catch { /* Preserve the original IO/cancellation error. */ }
        }
    }

    public async Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default)
    {
        var path = await ResolvePathAsync(key, cancellationToken);
        EnsureNoSymlinkPath(path);
        if (!File.Exists(path)) return null;
        try
        {
            return new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read,
                64 * 1024, FileOptions.Asynchronous | FileOptions.SequentialScan);
        }
        catch (FileNotFoundException)
        {
            return null;
        }
    }

    public async Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default)
    {
        var path = await ResolvePathAsync(key, cancellationToken);
        EnsureNoSymlinkPath(path);
        return File.Exists(path);
    }

    public async Task DeleteAsync(string key, CancellationToken cancellationToken = default)
    {
        var path = await ResolvePathAsync(key, cancellationToken);
        EnsureNoSymlinkPath(path);
        if (File.Exists(path))
        {
            EnsureNoSymlinkPath(path);
            File.Delete(path);
        }
    }

    public Task InitializeAsync(CancellationToken cancellationToken = default) => _initialization.Value.WaitAsync(cancellationToken);

    private async Task<string> ResolvePathAsync(string key, CancellationToken cancellationToken)
    {
        StorageKey.ValidateProviderKey(key);
        await _initialization.Value.WaitAsync(cancellationToken);
        var path = Path.GetFullPath(Path.Combine(_rootPath, key.Replace('/', Path.DirectorySeparatorChar)));
        var prefix = _rootPath.EndsWith(Path.DirectorySeparatorChar) ? _rootPath : _rootPath + Path.DirectorySeparatorChar;
        if (!path.StartsWith(prefix, StringComparison.Ordinal) || path.Equals(_rootPath, StringComparison.Ordinal))
            throw new ArgumentException("The storage key resolves outside the configured storage root.", nameof(key));
        EnsureNoSymlinkPath(path);
        return path;
    }

    private async Task InitializeCoreAsync()
    {
        if (_allowInsecureRootForDevelopment && !_isDevelopment)
        {
            throw new InvalidOperationException(
                "Storage configuration error: Storage:Local:AllowInsecureRootForDevelopment=true is only permitted in the Development environment. " +
                $"The current host environment is '{_environmentName}'. Disable the setting or use a secure local storage root.");
        }

        var existed = Directory.Exists(_rootPath);
        Directory.CreateDirectory(_rootPath);
        EnsureNoSymlinkPath(_rootPath);
        if (IsUnix() && existed && IsInsecureUnixDirectory(_rootPath) && !_allowInsecureRootForDevelopment)
        {
            throw new InvalidOperationException("The local storage root must not be group/world writable.");
        }
        if (IsUnix() && !existed)
            SetRestrictiveDirectoryMode(_rootPath);
        await Task.CompletedTask;
    }

    private void EnsureNoSymlinkPath(string path)
    {
        var fullPath = Path.GetFullPath(path);
        var relative = Path.GetRelativePath(_rootPath, fullPath);
        var current = _rootPath;
        CheckReparsePoint(current);
        if (relative == ".") return;

        foreach (var segment in relative.Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar))
        {
            current = Path.Combine(current, segment);
            CheckReparsePoint(current);
        }
    }

    private static void CheckReparsePoint(string path)
    {
        try
        {
            if (IsUnix())
            {
                if (new DirectoryInfo(path).LinkTarget is not null || new FileInfo(path).LinkTarget is not null)
                    throw new InvalidOperationException("Storage paths must not contain symbolic links or reparse points.");
            }
            else if ((File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0)
                throw new InvalidOperationException("Storage paths must not contain symbolic links or reparse points.");
        }
        catch (FileNotFoundException)
        {
        }
        catch (DirectoryNotFoundException)
        {
        }
    }

    private void EnsureSecureDirectoryPath(string path)
    {
        if (!IsUnix()) return;
        var relative = Path.GetRelativePath(_rootPath, Path.GetFullPath(path));
        var current = _rootPath;
        foreach (var segment in relative.Split(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar))
        {
            if (segment == "." || string.IsNullOrEmpty(segment)) continue;
            current = Path.Combine(current, segment);
            if (!Directory.Exists(current)) continue;
            if (IsInsecureUnixDirectory(current) && !_allowInsecureRootForDevelopment)
                throw new InvalidOperationException("Local storage directories must not be group/world writable.");
            if (!_allowInsecureRootForDevelopment)
                SetRestrictiveDirectoryMode(current);
        }
    }

    private readonly bool _allowInsecureRootForDevelopment;
    private readonly string _environmentName;
    private readonly bool _isDevelopment;

    private static bool IsInsecureUnixDirectory(string path) =>
        (GetUnixMode(path) & (UnixFileMode.GroupWrite | UnixFileMode.OtherWrite)) != 0;

    private static UnixFileMode GetUnixMode(string path) => File.GetUnixFileMode(path);

    private static void SetRestrictiveDirectoryMode(string path) =>
        File.SetUnixFileMode(path, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute);

    private static bool IsUnix() => !RuntimeInformation.IsOSPlatform(OSPlatform.Windows);

    private static LocalStorageOptions GetOptions(StorageOptions options)
    {
        options.Validate();
        return options.Local;
    }
}

#pragma warning restore CA1416
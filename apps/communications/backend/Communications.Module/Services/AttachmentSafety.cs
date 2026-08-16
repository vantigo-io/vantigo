using System.Security.Cryptography;

using Microsoft.Net.Http.Headers;

namespace Vantigo.Communications.Services;

internal static class AttachmentSafety
{
    internal static string SafeFileName(string? value)
    {
        var name = Path.GetFileName(string.IsNullOrWhiteSpace(value) ? "attachment" : value).Trim();
        var filtered = new string(name.Where(character => !char.IsControl(character) && character != '/' && character != '\\').ToArray());
        return string.IsNullOrWhiteSpace(filtered) ? "attachment" : filtered[..Math.Min(filtered.Length, 200)];
    }

    internal static string ContentType(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 200 || value.Any(char.IsControl)) return "application/octet-stream";
        try { return MediaTypeHeaderValue.Parse(value).MediaType.Value ?? "application/octet-stream"; }
        catch (FormatException) { return "application/octet-stream"; }
    }

    internal static string? ContentId(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 500 || value.Any(char.IsControl)) return null;
        var normalized = value.Trim().Trim('<', '>');
        return normalized.Length == 0 || normalized.Any(char.IsWhiteSpace) ? null : normalized;
    }
}

internal sealed class HashingReadStream(Stream inner, IncrementalHash hash, long limit) : Stream
{
    private long total;
    public override bool CanRead => inner.CanRead;
    public override bool CanSeek => false;
    public override bool CanWrite => false;
    public override long Length => total;
    public override long Position { get => total; set => throw new NotSupportedException(); }
    public override int Read(byte[] buffer, int offset, int count) => ReadAsync(buffer.AsMemory(offset, count)).AsTask().GetAwaiter().GetResult();
    public override async ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
    {
        var read = await inner.ReadAsync(buffer, cancellationToken);
        total += read;
        if (total > limit) throw new InvalidDataException("Attachment exceeds the configured limit.");
        if (read > 0) hash.AppendData(buffer.Span[..read]);
        return read;
    }
    public override Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken) => ReadAsync(buffer.AsMemory(offset, count), cancellationToken).AsTask();
    public override void Flush() => throw new NotSupportedException();
    public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
    public override void SetLength(long value) => throw new NotSupportedException();
    public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
}
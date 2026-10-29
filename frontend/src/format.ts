export function formatBytes(bytes: number): string {
  return formatSize(bytes, 1024);
}

// macOS disk capacity uses decimal units: 1 TB = 1,000 GB.
export function formatDiskBytes(bytes: number): string {
  return formatSize(bytes, 1000);
}

function formatSize(bytes: number, base: number): string {
  if (bytes < base) return `${bytes} B`;
  if (bytes < base ** 2) return `${(bytes / base).toFixed(1)} KB`;
  if (bytes < base ** 3) return `${(bytes / base ** 2).toFixed(1)} MB`;
  if (bytes < base ** 4) return `${(bytes / base ** 3).toFixed(1)} GB`;
  return `${(bytes / base ** 4).toFixed(1)} TB`;
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  if (bytes < 1024 ** 4) return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  return `${(bytes / 1024 ** 4).toFixed(1)} TB`;
}

// macOS disk capacity uses decimal units: 1 TB = 1,000 GB.
export function formatDiskBytes(bytes: number): string {
  if (bytes < 1000) return `${bytes} B`;
  if (bytes < 1000 ** 2) return `${(bytes / 1000).toFixed(1)} KB`;
  if (bytes < 1000 ** 3) return `${(bytes / 1000 ** 2).toFixed(1)} MB`;
  if (bytes < 1000 ** 4) return `${(bytes / 1000 ** 3).toFixed(1)} GB`;
  return `${(bytes / 1000 ** 4).toFixed(1)} TB`;
}

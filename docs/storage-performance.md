# Document storage layout comparison

Measured on October 29, 2026 on the development Intel MacBook, Docker Desktop / LinuxKit, Docker Engine 29.9.2 amd64, using the existing Go development container. These are storage-only observations, not complete HTTP transfer times, sustained-load benchmarks or predictions for a Mac mini.

The temporary harness called the actual storage layer: SHA-256 plus `Stage`/file sync, `Promote`/directory sync and stage cleanup for uploads; full size/SHA-256 verification followed by reading the bytes for downloads. SQLite, browser hashing and network transport were excluded because the proposed hybrid layout keeps SQLite in its current Docker volume. The comparison used a disposable named volume and a separate Mac bind-mounted directory, one layout at a time with alternating order, random synthetic data, one excluded warm-up and five measured runs per file size. The container ran as root for this probe; application-user bind-mount permissions were not validated. Reads had warm OS caches. No original vault data was read or moved.

| File | Docker upload median | Mac-folder upload median | Docker verified read median | Mac-folder verified read median |
| --- | --- | --- | --- | --- |
| 1 MiB | 20.086 ms | 11.138 ms | 2.959 ms | 9.150 ms |
| 10 MiB | 60.478 ms | 81.182 ms | 29.528 ms | 66.854 ms |
| 25 MiB | 140.059 ms | 172.391 ms | 74.551 ms | 126.716 ms |

For 25 MiB, the host folder took about 23% longer to store and 70% longer to verify/read, approximately 32 ms and 52 ms additional storage time. The 1 MiB upload result went the other way, illustrating why one small sample cannot justify a universal slowdown claim. Larger transfers, concurrency, cold caches, external disks, long-running durability and real ARM hardware have not been benchmarked. Named-volume and host-folder sync/permission semantics also need integration verification before a layout migration.

[Docker recommends named volumes for database I/O and documents host-file-sharing overhead](https://docs.docker.com/desktop/settings-and-maintenance/settings/#file-sharing). With upload/download speed as the current priority, preserve both SQLite and originals in Docker volumes. If live Finder browsing later becomes essential, keeping SQLite in a volume and moving originals to one Mac directory is a plausible design, with its staging/originals on the same filesystem for atomic publication; it needs representative end-to-end benchmarks and a verified migration first.

The temporary benchmark data and disposable volume were removed. The one-off harness was retained in the local OS temporary directory, not shipped as a product command. These observations are not a guarantee of equal performance across machines.

## Finder access and future folders

The user requires future folder hierarchies to be retrievable as a matching Finder folder tree. This requirement is recorded; folder support and export are not implemented in Checkpoint 6.

A directory tree can live inside a Docker named volume, but Finder does not directly browse that Linux VM filesystem. Docker Desktop can browse and save volume contents. Current originals use opaque IDs; SQLite stores original filenames. A bind mount alone would expose ID-named objects, not a friendly folder tree.

Future options are an explicit export that restores folder paths/original names as a separate copy, or a deliberately designed live host-folder layout for originals with SQLite kept in Docker. Neither follows automatically from creating virtual folders in SQLite. Manual renaming of current stored objects would break metadata/integrity checks. File uploads currently make a separate vault copy and leave the source unchanged; repeated uploads are not deduplicated.

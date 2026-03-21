# ufsd-utils — Host-Side Tools for UFS370 Disk Images

Create, inspect, and manipulate UFS370 filesystem images on macOS/Linux
without access to the mainframe. Intended as the standard tool for
off-host image provisioning — the alternative to MVS batch FORMAT jobs.

## Current Status

**Early development.** Core functionality works (create, info, ls, cp, cat, mkdir).
Missing: rm, indirect block support, fsck, comprehensive tests.

## Architecture

Single Go binary (`cmd/ufsd-utils/main.go`) with two reusable packages:

| Package | Purpose |
|---------|---------|
| `pkg/ufs/` | UFS370 on-disk format library (read/write images) |
| `pkg/ebcdic/` | EBCDIC CP037 ↔ ASCII conversion |

The `pkg/ufs/` library is designed for reuse — keep it clean and independent
of CLI concerns.

### Key Files

| File | Purpose |
|------|---------|
| `cmd/ufsd-utils/main.go` | CLI entry point, all subcommands |
| `pkg/ufs/types.go` | Constants, on-disk structures (boot, superblock, inode, dirent) |
| `pkg/ufs/image.go` | Image create/open, sector I/O, inode/directory read |
| `pkg/ufs/write.go` | File/directory creation, path resolution, directory entries |
| `pkg/ufs/alloc.go` | Block/inode allocation, free chain management |
| `pkg/ufs/timev.go` | Dual-format timestamp (V1 sec+usec / V2 time64) |
| `pkg/ebcdic/ebcdic.go` | CP037 translation tables and conversion functions |
| `doc/ufsdisk-spec.md` | **Authoritative** on-disk format specification |

## On-Disk Format

The format spec is in `doc/ufsdisk-spec.md` — read it before making
structural changes to `pkg/ufs/`.

Key facts:
- All multi-byte integers are **big-endian** (S/370 native)
- All strings on disk are **EBCDIC** (CP037)
- 128-byte inodes, 64-byte directory entries (59-char filenames)
- Block sizes 512–8192 (default 4096)
- V7-style free block chain in superblock
- Dual-format timestamps (auto-detected by useconds threshold)

## Known Limitations

- **Single indirect supported** — `CreateFile`/`ReadFileData` support
  addr[0..15] (direct) + addr[16] (single indirect), up to ~4 MB at 4096.
  Double/triple indirect addressing (addr[17..18]) is not yet implemented.
- **No overwrite** — `CreateFile` fails if the name already exists
- **No fsck** — no consistency checker yet

## CLI Commands

```
ufsd-utils create  <image> [--size N] [--blksize N]   Create + format image
ufsd-utils info    <image>                             Show image metadata
ufsd-utils ls [-l] <image[:/path]>                     List directory
ufsd-utils cp [-r] [-t|-b] <src> <dst>                 Copy host↔image
ufsd-utils cat [-b] <image[:/path]>                    Print file content
ufsd-utils mkdir [-p] <image[:/path]>                  Create directory
ufsd-utils rm   [-r]  <image[:/path]>                  Remove file or directory
ufsd-utils rmdir      <image[:/path]>                  Remove empty directory
ufsd-utils upload     <image> --dsn DSN                Upload image to MVS
```

Image paths accept: `img`, `img:`, `img:/`, `img:/path` — without path
defaults to root (/). For `cp`, the colon is required to distinguish
image paths from host paths.
Text mode (ASCII↔EBCDIC) is auto-detected by file extension.

## Coding Rules

- **Go 1.22+**, standard library only (no external dependencies)
- All disk I/O goes through `Image.ReadSector` / `Image.WriteSector`
- Big-endian helpers in `timev.go` (`getU32BE`, `putU32BE`, etc.) —
  use these, not `encoding/binary` directly in new code (except `image.go`
  which uses `binary.BigEndian` as `be` for historical reasons)
- EBCDIC conversion happens at the I/O boundary, not inside `pkg/ufs/`
  — the `ufs` package stores raw EBCDIC bytes in inode Owner/Group fields
- Keep `pkg/ufs/` free of `fmt.Print*` or `os.Exit` — errors bubble up
  to the CLI layer
- Comments and documentation in English

## Testing

Tests use Go's standard `testing` package. Target: high coverage for
`pkg/ufs/` and `pkg/ebcdic/`.

Test approach:
- Unit tests for EBCDIC conversion (`pkg/ebcdic/`)
- Round-trip tests for `pkg/ufs/`: create image → write files → read back → verify
- Table-driven tests where applicable
- Test files go next to the code: `pkg/ufs/image_test.go`, etc.

Run tests:
```
go test ./...
```

## Build & Run

```
go build -o ufsd-utils ./cmd/ufsd-utils
go test ./...
```

No external dependencies. No MVS connectivity required.

## Related Projects

- [ufsd](https://github.com/mvslovers/ufsd) — UFS370 filesystem server (MVS STC)
- [ufs370](https://github.com/mvslovers/ufs370) — Original UFS370 library (MVS, legacy)
- [ufs370-tools](https://github.com/mvslovers/ufs370-tools) — MVS-side FORMAT tool (legacy)

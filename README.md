# ufsd-utils

Host-side tools for UFS370 disk images.

Create, inspect, and manipulate UFS370 filesystem images used by the
[UFSD](https://github.com/mvslovers/ufsd) filesystem server on MVS 3.8j
(Hercules).

## Install

```bash
go install github.com/mvslovers/ufsd-utils/cmd/ufsd-utils@latest
```

Or build from source:
```bash
git clone https://github.com/mvslovers/ufsd-utils.git
cd ufsd-utils
go build -o ufsd-utils ./cmd/ufsd-utils
```

## Usage

```bash
# Create a new 10MB disk image
ufsd-utils create httpd-web.img --size 10M --blksize 4096

# Show image metadata
ufsd-utils info httpd-web.img

# List root directory
ufsd-utils ls httpd-web.img /
ufsd-utils ls -l httpd-web.img /

# (coming soon)
ufsd-utils cp  ./webroot/index.html httpd-web.img:/index.html
ufsd-utils cp  -r ./webroot/css/    httpd-web.img:/css/
ufsd-utils cat httpd-web.img:/index.html
ufsd-utils mkdir httpd-web.img:/newdir
ufsd-utils rm  httpd-web.img:/oldfile.txt
```

## Disk Format

UFS370 is a Unix V7-style filesystem adapted for MVS 3.8j. The on-disk
format specification is in [doc/ufsdisk-spec.md](doc/ufsdisk-spec.md).

Key characteristics:
- Block sizes: 512–8192 bytes (default 4096)
- 128-byte inodes with EBCDIC strings and dual-format timestamps
- 64-byte directory entries (59-char filenames)
- V7-style free block chain
- Big-endian byte order (S/370 native)

## Project Structure

```
cmd/ufsd-utils/     CLI entry point
internal/cli/       Cobra command implementations
pkg/ufs/            UFS370 format library (reusable)
pkg/ebcdic/         EBCDIC ↔ ASCII conversion
doc/                Format specification
```

## Related Projects

- [ufsd](https://github.com/mvslovers/ufsd) — Cross-AS filesystem server (MVS 3.8j)
- [ufs370](https://github.com/mvslovers/ufs370) — Original UFS370 library (MVS)
- [ufs370-tools](https://github.com/mvslovers/ufs370-tools) — MVS-side FORMAT tool

## License

MIT

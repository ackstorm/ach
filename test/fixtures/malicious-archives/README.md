# Malicious-archive fixtures (SAFE-01)

Deterministic `.tar.gz` fixtures for every SAFE-01 rejection class. The
Phase 7 `internal/cli/extract` package and the Phase 7 W4 end-to-end
safe-extract subtest consume these via the
`test/fixtures/malicious-archives` Go package (`maliciousfixtures`).

There are **no committed `.tar.gz` files in this directory** — the
fixtures are built on the fly inside `t.TempDir()` per test. This keeps
the repo lean (no large binary blobs in `git log`) and ensures the
fixtures track the source of truth (`fixtures.go`) automatically.

## Fixture set

| Filename                  | SAFE-01 class                                                                | Header detail                                       |
|---------------------------|------------------------------------------------------------------------------|-----------------------------------------------------|
| `absolute_path.tar.gz`    | Absolute path                                                                | `Name = "/etc/passwd"`                              |
| `dotdot.tar.gz`           | `..` segment                                                                 | `Name = "../escape.txt"`                            |
| `symlink_default.tar.gz`  | Symlink (default: rejected)                                                  | `Typeflag = TypeSymlink`, in-tree target            |
| `symlink_escape.tar.gz`   | Symlink target escapes `dst` (rejected even with `--allow-symlinks`)         | `Typeflag = TypeSymlink`, `Linkname = "../../..."`  |
| `hardlink.tar.gz`         | Hardlink (unconditional)                                                     | `Typeflag = TypeLink`                               |
| `device.tar.gz`           | Character device (unconditional)                                             | `Typeflag = TypeChar`                               |
| `fifo.tar.gz`             | Named pipe / FIFO (unconditional)                                            | `Typeflag = TypeFifo`                               |
| `pax_injection.tar.gz`    | Pax-extended-header path injection                                           | `PAXRecords["path"] = "/etc/escape"`                |

Every header carries `Uid=0`, `Gid=0`, `ModTime=time.Unix(0, 0).UTC()`
so the produced `.tar.gz` bytes are reproducible across runs and
machines. (`gzip` writes a small uncompressed-blob trailer that is also
deterministic for fixed input + default-compression-level.)

## Programmatic use (tests)

```go
import maliciousfixtures "github.com/ackstorm/ach/test/fixtures/malicious-archives"

paths, err := maliciousfixtures.BuildAll(t.TempDir())
if err != nil { t.Fatal(err) }
for _, name := range maliciousfixtures.Names {
    // paths[name] is the filesystem path to the fixture.
}
```

## Manual regeneration (debugging)

```bash
./scripts/dev.sh go run ./test/fixtures/malicious-archives/generator \
    ./test/fixtures/malicious-archives/
```

The generator is a thin wrapper around `BuildAll`. The output paths
are printed one per line for easy capture; the `.tar.gz` files land
under the supplied directory (or the current cwd when omitted).

Note: do **not** commit the generated `.tar.gz` files. They are listed
under `.gitignore` (via the repository-level `*.tar.gz` ignore rule
if present, and tests use `t.TempDir()` anyway). If you want to
diff them against another implementation, do it outside the worktree.

## References

- CLI spec §6.4 (tar safety table — every SAFE-01 rejection class enumerated)
- PRD D-11 (stdlib `archive/tar` + `compress/gzip`)
- SAFE-01..06 (REQUIREMENTS.md)
- `internal/cli/extract/tar.go` (the policy under test)

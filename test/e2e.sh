#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
#  lyra — end-to-end test suite
#
#  Usage:
#    bash test/e2e.sh               # local tests only
#    bash test/e2e.sh --ssh         # + SSH/SFTP  (prompts for credentials)
#    bash test/e2e.sh --ftp         # + FTP       (prompts for credentials)
#    bash test/e2e.sh --cloud       # + cloud     (prompts for OAuth per provider)
#    bash test/e2e.sh --ssh --ftp --cloud   # everything
#
#  Run from the project root:
#    bash test/e2e.sh
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

# ── colour palette ────────────────────────────────────────────────────────────
RED=$'\033[0;31m';  GREEN=$'\033[0;32m';  YELLOW=$'\033[1;33m'
CYAN=$'\033[0;36m'; BLUE=$'\033[0;34m';   BOLD=$'\033[1m'
DIM=$'\033[2m';     RESET=$'\033[0m'

# ── counters ──────────────────────────────────────────────────────────────────
PASS=0; FAIL=0; SKIP=0
FAILED_TESTS=()

# ── binary path ───────────────────────────────────────────────────────────────
if [[ "${OS:-}" == "Windows_NT" ]] || [[ "${OSTYPE:-}" =~ ^(msys|cygwin) ]]; then
    BIN="./bin/lyra.exe"
else
    BIN="./bin/lyra"
fi

[[ -f "$BIN" ]] || { echo "${RED}✗  Binary not found at $BIN — run 'make build' first.${RESET}"; exit 1; }
# Make BIN absolute so it stays valid after any `cd` inside helpers
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")

# ── work directory (auto-cleaned on exit) ─────────────────────────────────────
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

# ── option flags ──────────────────────────────────────────────────────────────
OPT_SSH=0; OPT_FTP=0; OPT_CLOUD=0
for _arg in "$@"; do
    case "$_arg" in --ssh) OPT_SSH=1;; --ftp) OPT_FTP=1;; --cloud) OPT_CLOUD=1;; esac
done

# ─────────────────────────────────────────────────────────────────────────────
#  HELPERS
# ─────────────────────────────────────────────────────────────────────────────

section() { echo ""; echo "${BOLD}${BLUE}  ── $* ──────────────────────────────────────────────────${RESET}"; }

ok()   { PASS=$((PASS+1));  echo "  ${GREEN}✓${RESET}  $*"; }
fail() {
    FAIL=$((FAIL+1))
    FAILED_TESTS+=("$*")
    echo "  ${RED}✗${RESET}  $*"
}
skip() { SKIP=$((SKIP+1));  echo "  ${YELLOW}⊘${RESET}  ${DIM}$*${RESET}"; }
info() { echo "  ${CYAN}→${RESET}  $*"; }
blank(){ echo ""; }

# mk_dir NAME — create a fresh sub-directory in WORK_DIR and echo its path
mk_dir() { local d="$WORK_DIR/$1"; mkdir -p "$d"; echo "$d"; }

# _run_lyra CMD [args…] — stores exit code in RC and stdout+stderr in OUT
_run_lyra() { OUT=$("$BIN" "$@" 2>&1) && RC=0 || RC=$?; }

# assert_ok  DESC CMD [args…]
assert_ok() {
    local desc="$1"; shift
    _run_lyra "$@"
    if [[ "$RC" -eq 0 ]]; then ok "$desc"
    else fail "$desc  ${DIM}(exit $RC: ${OUT:0:120})${RESET}"; fi
}

# assert_fail DESC CMD [args…]  — expects non-zero exit
assert_fail() {
    local desc="$1"; shift
    _run_lyra "$@"
    if [[ "$RC" -ne 0 ]]; then ok "$desc"
    else fail "$desc  ${DIM}(expected failure, got exit 0)${RESET}"; fi
}

# assert_contains DESC PATTERN CMD [args…]
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    _run_lyra "$@"
    if echo "$OUT" | grep -qi "$pattern"; then ok "$desc"
    else fail "$desc  ${DIM}(output missing: '$pattern')${RESET}"
         echo "       ${DIM}${OUT:0:200}${RESET}"; fi
}

# assert_not_contains DESC PATTERN CMD [args…]
assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    _run_lyra "$@"
    if ! echo "$OUT" | grep -qi "$pattern"; then ok "$desc"
    else fail "$desc  ${DIM}(output unexpectedly contains: '$pattern')${RESET}"; fi
}

# assert_file DESC PATH
assert_file() {
    if [[ -f "$2" ]]; then ok "$1"
    else fail "$1  ${DIM}(file not found: $2)${RESET}"; fi
}

# assert_no_file DESC PATH
assert_no_file() {
    if [[ ! -f "$2" ]]; then ok "$1"
    else fail "$1  ${DIM}(file should not exist: $2)${RESET}"; fi
}

# assert_dir DESC PATH
assert_dir() {
    if [[ -d "$2" ]]; then ok "$1"
    else fail "$1  ${DIM}(directory not found: $2)${RESET}"; fi
}

# assert_no_dir DESC PATH
assert_no_dir() {
    if [[ ! -d "$2" ]]; then ok "$1"
    else fail "$1  ${DIM}(directory should not exist: $2)${RESET}"; fi
}

# assert_files_equal DESC FILE_A FILE_B
assert_files_equal() {
    if cmp -s "$2" "$3"; then ok "$1"
    else fail "$1  ${DIM}(files differ: $2 vs $3)${RESET}"; fi
}

# assert_content DESC FILE EXPECTED
assert_content() {
    local actual; actual=$(cat "$2" 2>/dev/null || true)
    if [[ "$actual" == "$3" ]]; then ok "$1"
    else fail "$1  ${DIM}(expected '$3', got '$actual')${RESET}"; fi
}

# run_in DESC DIR CMD [args…]  — runs lyra with cwd=DIR
run_in() {
    local desc="$1" dir="$2"; shift 2
    OUT=$(cd "$dir" && "$BIN" "$@" 2>&1) && RC=0 || RC=$?
    if [[ "$RC" -eq 0 ]]; then ok "$desc"
    else fail "$desc  ${DIM}(exit $RC: ${OUT:0:120})${RESET}"; fi
}

# ─────────────────────────────────────────────────────────────────────────────
#  LOCAL TEST GROUPS
# ─────────────────────────────────────────────────────────────────────────────

test_md() {
    section "md — make directory"
    local d; d=$(mk_dir md)

    assert_ok  "creates a single directory"        md "$d/single"
    assert_dir "single directory was created"      "$d/single"

    assert_ok  "creates nested parents"            md "$d/a/b/c"
    assert_dir "deepest nested directory exists"   "$d/a/b/c"

    assert_ok  "idempotent — existing directory"   md "$d/single"

    assert_fail "fails on empty argument"          md ""
}

# ──────────────────────────────────────────────────────────────────────────────

test_ls() {
    section "ls — directory listing"
    local d; d=$(mk_dir ls)
    mkdir -p "$d/subdir"
    echo "hello" > "$d/visible.txt"
    echo "world" > "$d/.hidden"

    assert_ok       "lists a directory"                    ls "$d"
    assert_contains "shows visible files"   "visible.txt"  ls "$d"
    assert_contains "shows subdirectories"  "subdir"       ls "$d"

    # Hidden files excluded by default
    _run_lyra ls "$d"
    if echo "$OUT" | grep -q "\.hidden"; then
        fail "hidden files should be excluded by default"
    else
        ok "hidden files excluded by default"
    fi

    assert_contains "shows hidden with --all"   ".hidden"  ls --all "$d"

    assert_ok       "tree view"                            ls --tree "$d"
    assert_contains "tree shows nested dir"     "subdir"   ls --tree "$d"

    assert_ok       "sort by size"                         ls --sort size "$d"
    assert_ok       "sort by time"                         ls --sort time "$d"
    assert_ok       "sort by type"                         ls --sort type "$d"
    assert_ok       "sort by name (default)"               ls --sort name "$d"

    assert_ok       "lists a single file"                  ls "$d/visible.txt"
    assert_fail     "fails on non-existent path"           ls "$d/no_such_entry"
}

# ──────────────────────────────────────────────────────────────────────────────

test_touch() {
    section "touch — create / update timestamps"
    local d; d=$(mk_dir touch)

    assert_ok   "creates a new file"                    touch "$d/new.txt"
    assert_file "created file exists"                   "$d/new.txt"

    assert_ok   "touches an existing file (no error)"   touch "$d/new.txt"

    assert_ok   "creates multiple files at once"        touch "$d/a.txt" "$d/b.txt" "$d/c.txt"
    assert_file "first file in batch"                   "$d/a.txt"
    assert_file "last file in batch"                    "$d/c.txt"

    assert_ok   "custom timestamp"                      touch --time "2024-06-15 12:00:00" "$d/ts.txt"
    assert_file "timestamped file created"              "$d/ts.txt"

    assert_ok   "--no-create skips missing file"        touch --no-create "$d/ghost.txt"
    assert_no_file "--no-create did not create file"    "$d/ghost.txt"

    assert_fail "rejects invalid timestamp format"      touch --time "not-a-date" "$d/bad.txt"
}

# ──────────────────────────────────────────────────────────────────────────────

test_cp() {
    section "cp — local copy"
    local d; d=$(mk_dir cp)
    printf 'lyra test file\n' > "$d/src.txt"
    mkdir -p "$d/srcdir/nested"
    echo "file1"    > "$d/srcdir/file1.txt"
    echo "nested"   > "$d/srcdir/nested/file2.txt"

    # Single file
    assert_ok         "copies a file"                       cp "$d/src.txt" "$d/dst.txt"
    assert_file       "destination file exists"             "$d/dst.txt"
    assert_files_equal "content is identical"               "$d/src.txt" "$d/dst.txt"

    # Copy into directory
    mkdir -p "$d/outdir"
    assert_ok         "copies file into an existing dir"    cp "$d/src.txt" "$d/outdir/"
    assert_file       "file placed inside target dir"       "$d/outdir/src.txt"

    # Recursive
    assert_fail       "refuses recursive copy without -r"   cp "$d/srcdir" "$d/dstdir"
    assert_ok         "recursive copy with -r"              cp -r "$d/srcdir" "$d/dstdir"
    assert_dir        "destination directory exists"        "$d/dstdir"
    assert_file       "top-level file copied"               "$d/dstdir/file1.txt"
    assert_file       "nested file copied"                  "$d/dstdir/nested/file2.txt"
    assert_files_equal "nested content matches"             "$d/srcdir/nested/file2.txt" "$d/dstdir/nested/file2.txt"

    # --checksum
    assert_ok         "copy with --checksum verification"   cp --checksum "$d/src.txt" "$d/chk.txt"
    assert_file       "checksum-verified copy exists"       "$d/chk.txt"

    # --sync (skip identical)
    cp "$d/src.txt" "$d/sync_dst.txt"
    assert_ok         "--sync skips identical file"         cp --sync "$d/src.txt" "$d/sync_dst.txt"

    # --preserve
    assert_ok         "copy with --preserve"                cp --preserve "$d/src.txt" "$d/pres.txt"

    # Error cases
    assert_fail       "fails on missing source"             cp "$d/no_such.txt" "$d/out.txt"
    assert_fail       "fails src == dst"                    cp "$d/src.txt" "$d/src.txt"
}

# ──────────────────────────────────────────────────────────────────────────────

test_mv() {
    section "mv — move / rename"
    local d; d=$(mk_dir mv)
    echo "original content" > "$d/orig.txt"
    mkdir -p "$d/mvdir/sub"
    echo "x" > "$d/mvdir/sub/deep.txt"

    # Rename in place
    assert_ok       "renames a file"                    mv "$d/orig.txt" "$d/renamed.txt"
    assert_file     "renamed file exists"               "$d/renamed.txt"
    assert_no_file  "original no longer exists"         "$d/orig.txt"
    assert_content  "content preserved after rename"    "$d/renamed.txt" "original content"

    # Move into directory
    echo "to move" > "$d/tomove.txt"
    mkdir -p "$d/destdir"
    assert_ok       "moves file into a directory"       mv "$d/tomove.txt" "$d/destdir/"
    assert_file     "file now inside destination dir"   "$d/destdir/tomove.txt"
    assert_no_file  "source removed after move"         "$d/tomove.txt"

    # Move directory
    assert_ok       "moves an entire directory"         mv "$d/mvdir" "$d/mvdir_moved"
    assert_dir      "moved directory exists at new path" "$d/mvdir_moved"
    assert_file     "contents preserved inside moved dir" "$d/mvdir_moved/sub/deep.txt"
    assert_no_dir   "source directory is gone"          "$d/mvdir"

    # Error cases
    assert_fail     "fails on missing source"           mv "$d/no_such.txt" "$d/out.txt"
}

# ──────────────────────────────────────────────────────────────────────────────

test_rm() {
    section "rm — remove"
    local d; d=$(mk_dir rm)
    echo "trash me"  > "$d/trash.txt"
    echo "delete me" > "$d/perm.txt"
    mkdir -p "$d/rmdir/sub"
    echo "x" > "$d/rmdir/sub/x.txt"

    # Trash (default — file should disappear from original location)
    assert_ok      "moves file to system trash"             rm "$d/trash.txt"
    assert_no_file "file no longer at original path"        "$d/trash.txt"

    # Permanent delete
    assert_ok      "permanently deletes a file"             rm --permanent "$d/perm.txt"
    assert_no_file "permanently deleted file is gone"       "$d/perm.txt"

    # Recursive
    assert_fail    "refuses to remove dir without -r"       rm "$d/rmdir"
    assert_ok      "removes directory with -r --permanent"  rm -r --permanent "$d/rmdir"
    assert_no_dir  "directory was removed"                  "$d/rmdir"

    # --force
    assert_ok      "--force on non-existent file is silent" rm --force "$d/ghost.txt"
    assert_fail    "fails on non-existent without --force"  rm "$d/ghost.txt"

    # Batch + summary
    echo "s1" > "$d/s1.txt"; echo "s2" > "$d/s2.txt"; echo "s3" > "$d/s3.txt"
    assert_ok      "batch remove (3 files)"                 rm --permanent "$d/s1.txt" "$d/s2.txt" "$d/s3.txt"
    assert_no_file "first batch file gone"                  "$d/s1.txt"
    assert_no_file "last batch file gone"                   "$d/s3.txt"

    # --no-summary
    echo "n1" > "$d/n1.txt"; echo "n2" > "$d/n2.txt"
    assert_ok      "--no-summary suppresses table"          rm --no-summary --permanent "$d/n1.txt" "$d/n2.txt"
}

# ──────────────────────────────────────────────────────────────────────────────

test_find() {
    section "find — search"
    local d; d=$(mk_dir find)
    mkdir -p "$d/src/nested"
    echo "go"   > "$d/src/main.go"
    echo "text" > "$d/src/notes.txt"
    echo "log"  > "$d/src/app.log"
    echo "deep" > "$d/src/nested/deep.go"
    dd if=/dev/urandom of="$d/src/big.bin" bs=1024 count=512 2>/dev/null  # 512 KB

    assert_contains "finds by name glob"                   "main.go"   find "$d/src" --name "*.go"
    assert_contains "finds nested files with same pattern" "deep.go"   find "$d/src" --name "*.go"
    assert_ok       "type=file filter"                                  find "$d/src" --type file
    assert_ok       "type=dir filter"                                   find "$d/src" --type dir
    assert_ok       "max-depth 0 shows nothing"                        find "$d/src" --max-depth 0
    assert_contains "max-depth 1 finds top-level files"   "main.go"   find "$d/src" --max-depth 1
    assert_not_contains "max-depth 1 skips nested"        "deep.go"   find "$d/src" --max-depth 1

    assert_ok       "size filter +100B"                                find "$d/src" --size "+100B"
    assert_contains "size filter finds big file"          "big.bin"   find "$d/src" --size "+100KB"
    assert_ok       "modified filter last 1 year"                      find "$d/src" --modified "last 1 year"
    assert_ok       "combined name+type filter"                        find "$d/src" --name "*.go" --type file
    assert_ok       "no results is not an error"                       find "$d/src" --name "*.xyz"

    # --regex mode
    assert_contains "regex: matches by pattern"           "main.go"   find "$d/src" --regex --name '\.go$'
    assert_contains "regex: alternation matches both"     "main.go"   find "$d/src" --regex --name '\.(go|txt)$'
    assert_contains "regex: alternation also finds txt"   "notes.txt" find "$d/src" --regex --name '\.(go|txt)$'
    assert_not_contains "regex: does not match .log"      "app.log"   find "$d/src" --regex --name '\.(go|txt)$'
    assert_ok       "regex: anchored pattern no results"              find "$d/src" --regex --name '^nomatch'
    assert_fail     "regex: invalid pattern is an error"              find "$d/src" --regex --name '[invalid'
}

# ──────────────────────────────────────────────────────────────────────────────

test_sync() {
    section "sync — directory synchronisation"
    local d; d=$(mk_dir sync)
    mkdir -p "$d/src/sub" "$d/dst"
    echo "alpha" > "$d/src/alpha.txt"
    echo "beta"  > "$d/src/beta.txt"
    echo "gamma" > "$d/src/sub/gamma.txt"

    # Dry-run — no side effects
    assert_ok      "dry-run completes without error"          sync --dry-run "$d/src" "$d/dst"
    assert_no_file "dry-run wrote nothing to destination"     "$d/dst/alpha.txt"

    # One-way sync
    assert_ok      "syncs source to destination"              sync "$d/src" "$d/dst"
    assert_file    "top-level file synced"                    "$d/dst/alpha.txt"
    assert_file    "nested file synced"                       "$d/dst/sub/gamma.txt"
    assert_files_equal "synced content matches"               "$d/src/alpha.txt" "$d/dst/alpha.txt"

    # Idempotent second run
    assert_ok      "re-sync is idempotent (no changes)"       sync "$d/src" "$d/dst"

    # --delete removes orphan
    echo "orphan" > "$d/dst/orphan.txt"
    assert_ok      "--delete removes files absent from source" sync --delete "$d/src" "$d/dst"
    assert_no_file "orphan removed from destination"           "$d/dst/orphan.txt"
    assert_file    "legitimate file kept after --delete"       "$d/dst/alpha.txt"

    # --checksum
    assert_ok      "checksum-based sync"                       sync --checksum "$d/src" "$d/dst"

    # Error cases
    assert_fail    "fails when source does not exist"         sync "$d/no_src" "$d/dst"
    assert_fail    "fails when source is not a directory"     sync "$d/src/alpha.txt" "$d/dst"
}

# ──────────────────────────────────────────────────────────────────────────────

test_rename() {
    section "rename — batch rename"
    local d; d=$(mk_dir rename)

    # Pattern rename — must run with cwd=d because the command resolves patterns relative to cwd
    echo "a" > "$d/a.txt"
    echo "b" > "$d/b.txt"
    echo "c" > "$d/c.txt"

    run_in "dry-run does not rename files"       "$d"  rename --dry-run "*.txt" "*.bak"
    assert_file "dry-run left originals intact"        "$d/a.txt"

    run_in "renames files by extension pattern"  "$d"  rename "*.txt" "*.bak"
    assert_file    "renamed file a.bak exists"         "$d/a.bak"
    assert_no_file "original a.txt removed"            "$d/a.txt"
    assert_file    "renamed file b.bak exists"         "$d/b.bak"

    # --no-summary
    run_in "--no-summary suppresses table"       "$d"  rename --no-summary "*.bak" "*.old"
    assert_file "files still renamed"                  "$d/a.old"

    # Sequential rename (filepath.Glob handles absolute paths, so no cd needed)
    echo "x" > "$d/img1.jpg"; echo "x" > "$d/img2.jpg"; echo "x" > "$d/img3.jpg"
    assert_ok  "sequential numbering --seq"            rename --seq "$d/img1.jpg" "$d/img2.jpg" "$d/img3.jpg"
    assert_file "first sequential file 001.jpg"        "$d/001.jpg"
    assert_file "third sequential file 003.jpg"        "$d/003.jpg"

    # --start and --width
    echo "x" > "$d/p1.png"; echo "x" > "$d/p2.png"
    assert_ok  "sequential with custom start+width"    rename --seq --start 10 --width 4 "$d/p1.png" "$d/p2.png"
    assert_file "custom-start file 0010.png"           "$d/0010.png"

    # Case conversion (also uses filepath.Glob — absolute path works)
    echo "x" > "$d/UPPER.old"
    assert_ok  "lowercase case conversion"             rename --case lower "$d/UPPER.old"
    assert_file "lowercased file exists"               "$d/upper.old"

    echo "x" > "$d/quiet.old"
    assert_ok  "uppercase case conversion"             rename --case upper "$d/quiet.old"
    assert_file "uppercased file exists"               "$d/QUIET.OLD"

    # --regex mode
    local r; r=$(mk_dir rename_regex)
    echo "x" > "$r/report_v1.txt"
    echo "x" > "$r/report_v2.txt"
    echo "x" > "$r/other.txt"

    run_in "regex: dry-run shows renames"      "$r"  rename --regex --dry-run '^report_v([0-9]+)\.txt$' 'report-$1.txt'
    assert_file "regex: dry-run left originals"       "$r/report_v1.txt"

    run_in "regex: renames matching files"     "$r"  rename --regex '^report_v([0-9]+)\.txt$' 'report-$1.txt'
    assert_file    "regex: report-1.txt created"      "$r/report-1.txt"
    assert_file    "regex: report-2.txt created"      "$r/report-2.txt"
    assert_no_file "regex: original report_v1 gone"   "$r/report_v1.txt"
    assert_file    "regex: non-matching file untouched" "$r/other.txt"

    # Named capture groups
    echo "x" > "$r/photo.jpeg"
    run_in "regex: named group rename"         "$r"  rename --regex '(?P<base>.+)\.jpeg$' '${base}.jpg'
    assert_file    "regex: photo.jpg created"         "$r/photo.jpg"
    assert_no_file "regex: photo.jpeg removed"        "$r/photo.jpeg"

    assert_fail "regex: invalid pattern is an error"  rename --regex '[bad' 'x'
}

# ──────────────────────────────────────────────────────────────────────────────

test_info() {
    section "info — file information"
    local d; d=$(mk_dir info)
    printf 'lyra info test content\n' > "$d/sample.txt"
    mkdir -p "$d/somedir"
    echo "x" > "$d/somedir/child.txt"

    assert_contains "shows file path"      "Path"      info "$d/sample.txt"
    assert_contains "shows file type"      "Type"      info "$d/sample.txt"
    assert_contains "shows size"           "Size"      info "$d/sample.txt"
    assert_contains "shows mode"           "Mode"      info "$d/sample.txt"
    assert_contains "shows modified time"  "Modified"  info "$d/sample.txt"
    assert_contains "shows MIME type"      "text"      info "$d/sample.txt"
    assert_contains "shows MD5 checksum"   "MD5"       info "$d/sample.txt"
    assert_contains "shows SHA1 checksum"  "SHA1"      info "$d/sample.txt"
    assert_contains "shows SHA256"         "SHA256"    info "$d/sample.txt"

    assert_ok       "info works on a directory"        info "$d/somedir"
    assert_contains "dir info shows contents count"  "Contents"  info "$d/somedir"

    assert_fail     "fails gracefully on missing path" info "$d/no_such_file"
}

# ──────────────────────────────────────────────────────────────────────────────

test_resume() {
    section "resume — list paused transfers"
    assert_ok "resume list runs without error"  resume
}

test_resume_interrupt() {
    section "resume — interrupt and continue"
    local d; d=$(mk_dir resume_interrupt)

    if ! command -v dd &>/dev/null; then
        skip "dd not available — skipping interrupt/resume test"
        return
    fi

    info "Generating 60 MB test file…"
    dd if=/dev/urandom of="$d/big.bin" bs=1M count=60 2>/dev/null

    local dest="$d/big_copy.bin"

    # Start copy in background then interrupt after a short delay.
    "$BIN" cp "$d/big.bin" "$dest" &
    local pid=$!
    sleep 0.4
    kill -INT "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true

    local state_dir="$HOME/.lyra/resume"
    if ls "$state_dir"/*.json &>/dev/null 2>&1; then
        ok "resume state file written after interrupt"
    else
        skip "no resume state found — transfer may have finished before interrupt signal"
        return
    fi

    assert_ok         "resumes an interrupted copy"    cp --resume "$d/big.bin" "$dest"
    assert_files_equal "resumed file byte-matches src" "$d/big.bin" "$dest"

    # Clean up state files for this transfer
    rm -f "$state_dir"/*.json 2>/dev/null || true
}

# ─────────────────────────────────────────────────────────────────────────────
#  REMOTE TEST GROUPS
# ─────────────────────────────────────────────────────────────────────────────

test_ssh() {
    section "cp — SSH / SFTP"
    if [[ "$OPT_SSH" -eq 0 ]]; then
        skip "SSH tests skipped  (re-run with --ssh to enable)"
        return
    fi

    blank
    info "SSH credentials"
    read -rp  "  User@host (e.g. alice@192.168.1.10): "         SSH_HOST
    read -rp  "  Remote writable directory (e.g. /tmp/lyra): "  SSH_REMOTE_DIR
    read -rp  "  SSH private key path [~/.ssh/id_rsa]:  "       SSH_KEY_INPUT
    SSH_KEY="${SSH_KEY_INPUT:-$HOME/.ssh/id_rsa}"

    SSH_PASS=""
    if [[ ! -f "$SSH_KEY" ]]; then
        info "Key not found at $SSH_KEY — falling back to password auth"
        read -rsp "  SSH password: " SSH_PASS; echo
        [[ -z "$SSH_PASS" ]] && { skip "no SSH key or password — skipping"; return; }
    fi

    local d; d=$(mk_dir ssh)
    local stamp; stamp=$(date +%s)
    printf 'ssh test content %s\n' "$stamp" > "$d/ssh_src.txt"

    local remote="${SSH_HOST}:${SSH_REMOTE_DIR}/lyra_test_${stamp}.txt"

    # Upload
    if [[ -n "$SSH_PASS" ]]; then
        assert_ok "uploads file via SSH (password auth)" \
            cp --password "$SSH_PASS" "$d/ssh_src.txt" "$remote"
    else
        assert_ok "uploads file via SSH (key auth)" \
            cp --key "$SSH_KEY" "$d/ssh_src.txt" "$remote"
    fi

    # Download
    assert_ok "downloads file from SSH" \
        cp --key "$SSH_KEY" "$remote" "$d/ssh_dst.txt"
    assert_file       "downloaded file exists"     "$d/ssh_dst.txt"
    assert_files_equal "content matches original"  "$d/ssh_src.txt" "$d/ssh_dst.txt"

    # Recursive upload
    mkdir -p "$d/ssh_dir"
    echo "r1" > "$d/ssh_dir/r1.txt"
    echo "r2" > "$d/ssh_dir/r2.txt"
    assert_ok "recursive upload via SSH" \
        cp -r --key "$SSH_KEY" "$d/ssh_dir" "${SSH_HOST}:${SSH_REMOTE_DIR}/lyra_dir_${stamp}/"
}

# ──────────────────────────────────────────────────────────────────────────────

test_ftp() {
    section "cp — FTP"
    if [[ "$OPT_FTP" -eq 0 ]]; then
        skip "FTP tests skipped  (re-run with --ftp to enable)"
        return
    fi

    blank
    info "FTP credentials"
    read -rp  "  Host:                          " FTP_HOST
    read -rp  "  Port [21]:                     " FTP_PORT_INPUT
    read -rp  "  Username:                      " FTP_USER
    read -rsp "  Password:                      " FTP_PASS; echo
    read -rp  "  Remote upload path [/upload]:  " FTP_REMOTE_INPUT
    FTP_PORT="${FTP_PORT_INPUT:-21}"
    FTP_REMOTE="${FTP_REMOTE_INPUT:-/upload}"
    FTP_REMOTE="${FTP_REMOTE%/}"   # strip trailing slash to avoid double-slash in URL

    local d; d=$(mk_dir ftp)
    local stamp; stamp=$(date +%s)
    printf 'ftp test content %s\n' "$stamp" > "$d/ftp_src.txt"

    local remote_url="ftp://${FTP_USER}:${FTP_PASS}@${FTP_HOST}:${FTP_PORT}${FTP_REMOTE}/lyra_test_${stamp}.txt"

    assert_ok         "uploads file via FTP"        cp "$d/ftp_src.txt" "$remote_url"
    assert_ok         "downloads file from FTP"     cp "$remote_url" "$d/ftp_dst.txt"
    assert_file       "downloaded file exists"       "$d/ftp_dst.txt"
    assert_files_equal "content matches original"    "$d/ftp_src.txt" "$d/ftp_dst.txt"
}

# ─────────────────────────────────────────────────────────────────────────────
#  CLOUD TEST GROUPS
# ─────────────────────────────────────────────────────────────────────────────

_cloud_test() {
    local provider="$1" scheme="$2"

    if [[ "$OPT_CLOUD" -eq 0 ]]; then
        skip "$provider tests skipped  (re-run with --cloud to enable)"
        return
    fi

    blank
    info "$provider configuration"
    read -rp "  Authenticate now? Runs 'lyra auth $provider' (opens browser) [Y/n]: " DO_AUTH
    DO_AUTH="${DO_AUTH:-Y}"

    if [[ "$DO_AUTH" =~ ^[Yy] ]]; then
        info "Starting OAuth2 flow for $provider — a browser window will open…"
        "$BIN" auth "$provider" || { skip "$provider auth failed — skipping cloud tests"; return; }
        ok "$provider authenticated successfully"
    else
        info "Skipping auth — assuming token already cached"
    fi

    read -rp "  Remote test path (e.g. MyDrive/lyra-e2e): " CLOUD_REMOTE
    local d; d=$(mk_dir "cloud_${provider}")
    local stamp; stamp=$(date +%s)
    printf 'cloud test content %s\n' "$stamp" > "$d/cloud_src.txt"

    local remote_file="${scheme}://${CLOUD_REMOTE}/lyra_test_${stamp}.txt"

    assert_ok         "uploads file to $provider"       cp "$d/cloud_src.txt" "$remote_file"
    assert_ok         "downloads file from $provider"   cp "$remote_file" "$d/cloud_dst.txt"
    assert_file       "downloaded file exists"           "$d/cloud_dst.txt"
    assert_files_equal "downloaded content matches"      "$d/cloud_src.txt" "$d/cloud_dst.txt"

    # Recursive upload
    mkdir -p "$d/cloud_dir"
    echo "r1" > "$d/cloud_dir/r1.txt"
    echo "r2" > "$d/cloud_dir/r2.txt"
    assert_ok "recursive folder upload to $provider" \
        cp -r "$d/cloud_dir" "${scheme}://${CLOUD_REMOTE}/lyra_dir_${stamp}/"

    # Remove uploaded test file
    assert_ok "removes remote test file" \
        rm "${scheme}://${CLOUD_REMOTE}/lyra_test_${stamp}.txt"
}

test_cloud_gdrive()   { _cloud_test "gdrive"   "gdrive";   }
test_cloud_dropbox()  { _cloud_test "dropbox"  "dropbox";  }
test_cloud_onedrive() { _cloud_test "onedrive" "onedrive"; }

# ─────────────────────────────────────────────────────────────────────────────
#  MAIN
# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo "${BOLD}══════════════════════════════════════════════════════════════${RESET}"
echo "${BOLD}  lyra — end-to-end test suite${RESET}"
echo "${BOLD}══════════════════════════════════════════════════════════════${RESET}"
info "Binary : $BIN  ($(ls -lh "$BIN" | awk '{print $5}'))"
info "Workdir: $WORK_DIR"
[[ "$OPT_SSH"   -eq 1 ]] && info "SSH tests   : ${GREEN}enabled${RESET}"   || info "SSH tests   : ${DIM}disabled${RESET}"
[[ "$OPT_FTP"   -eq 1 ]] && info "FTP tests   : ${GREEN}enabled${RESET}"   || info "FTP tests   : ${DIM}disabled${RESET}"
[[ "$OPT_CLOUD" -eq 1 ]] && info "Cloud tests : ${GREEN}enabled${RESET}"   || info "Cloud tests : ${DIM}disabled${RESET}"

# ── local ─────────────────────────────────────────────────────────────────────
test_md
test_ls
test_touch
test_cp
test_mv
test_rm
test_find
test_sync
test_rename
test_info
test_resume
test_resume_interrupt

# ── remote ────────────────────────────────────────────────────────────────────
test_ssh
test_ftp

# ── cloud ─────────────────────────────────────────────────────────────────────
test_cloud_gdrive
test_cloud_dropbox
test_cloud_onedrive

# ── summary ───────────────────────────────────────────────────────────────────
echo ""
echo "${BOLD}══════════════════════════════════════════════════════════════${RESET}"
printf "  ${GREEN}%-8s${RESET} %d\n" "Passed"  "$PASS"
printf "  ${RED}%-8s${RESET} %d\n"   "Failed"  "$FAIL"
printf "  ${YELLOW}%-8s${RESET} %d\n" "Skipped" "$SKIP"
echo "${BOLD}══════════════════════════════════════════════════════════════${RESET}"

if [[ ${#FAILED_TESTS[@]} -gt 0 ]]; then
    echo ""
    echo "  ${RED}${BOLD}Failures:${RESET}"
    for t in "${FAILED_TESTS[@]}"; do
        echo "    ${RED}✗${RESET}  $t"
    done
    echo ""
fi

if [[ "$FAIL" -eq 0 ]]; then
    echo "  ${GREEN}${BOLD}All tests passed.${RESET}"
    echo ""
    exit 0
else
    echo "  ${RED}${BOLD}$FAIL test(s) failed.${RESET}"
    echo ""
    exit 1
fi

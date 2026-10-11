#!/usr/bin/env bash
# s4.sh: the mechanical part of the infrastructure split. Run it from the repository root on a tree that still has
# internal/port and internal/adapter; it is idempotent (moves are skipped once done). It
#   1. moves internal/port into internal/store, internal/ai, internal/blob and internal/clock, and the adapters into
#      internal/store/postgres, internal/blob/s3 and internal/ai/gateway;
#   2. rewrites every port.X reference to its new home (ids and model types to domain) and fixes the imports;
#   3. renames postgres.Store to postgres.DB, s3.Blob to s3.Store (s3.NewBlob to s3.New) and fake.Clock to
#      clock.Manual;
#   4. deletes the engine and server type aliases, rewriting their uses to the aliased types;
#   5. renames the locals that the new package names would shadow;
#   6. rewrites internal/adapter/* path mentions outside docs/.
# It is safe to re-run on its own output (every step is a no-op once done). The hand-written part (the Reader split,
# store.Maintenance, ai.Router and Tokenizer, blob.Key and blobtest, clock.System, config.Resolver, the wire split,
# embedded-field and test fixes, docs) sits on top of its output in the same commit. On a conflict: start from the
# new base, run this script, `make gen`, then re-apply the stage commit's hand-written changes (its diff against a
# fresh script run) and resolve what remains with `go vet ./...`. This script is deleted once the restructure is
# finished.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

mv_if() { # mv_if SRC DST: git mv when SRC still exists
	if [ -e "$1" ]; then
		mkdir -p "$(dirname "$2")"
		git mv "$1" "$2"
	fi
}
rm_if() { if [ -e "$1" ]; then git rm -q -r "$1"; fi; }

mv_if internal/adapter/postgres internal/store/postgres
mv_if internal/adapter/s3 internal/blob/s3
mv_if internal/adapter/gateway internal/ai/gateway
mv_if internal/adapter/AGENTS.md internal/store/AGENTS.md
mv_if internal/port/store.go internal/store/db.go
mv_if internal/port/repos.go internal/store/repos.go
mv_if internal/port/jobs.go internal/store/queue.go
mv_if internal/port/queries.go internal/store/queries.go
mv_if internal/port/queries_test.go internal/store/queries_test.go
mv_if internal/port/doc.go internal/store/doc.go
mv_if internal/port/models.go internal/ai/ai.go
mv_if internal/port/blob.go internal/blob/blob.go
mv_if internal/port/clock.go internal/clock/clock.go
mv_if internal/fake/clock.go internal/clock/manual.go
mv_if internal/engine/routing/router.go internal/ai/router.go
mv_if internal/engine/routing/router_test.go internal/ai/router_test.go
mv_if cmd/memd/tokens.go internal/ai/tokenizer.go
mv_if internal/blob/s3/blob_cases_test.go internal/blob/blobtest/blobtest.go
rm_if internal/port
rm_if internal/engine/routing

# Path mentions in comments, docs and config outside docs/ (the docs stage rewrites docs/).
git ls-files -z -- . ':!CLAUDE.md' ':!docs/**' ':!scripts/restructure/**' ':!gen/**' ':!internal/mocks/**' |
	xargs -0 grep -lI --null -e 'internal/adapter/' 2>/dev/null |
	xargs -0 perl -pi -e 's{internal/adapter/postgres}{internal/store/postgres}g; s{internal/adapter/s3}{internal/blob/s3}g;
		s{internal/adapter/gateway}{internal/ai/gateway}g; s{internal/adapter/AGENTS\.md}{internal/store/AGENTS.md}g' || true

python3 - <<'PY'
import os
import re
import subprocess

# ---------- tables ----------

STORE = """DB Tx Reader Queue Claimer Job DedupePolicy DedupeNone DedupePending DedupePendingOrRunning OperationFilter
LostJobsError LostJobs SeqRange SeqUpTo PredicateOp PredicateTags PredicateAnd PredicateOr PredicateNot SQLPredicate
VectorQuery TextQuery TemporalQuery GraphQuery Hit ScopeQuery ModelQuery CronModel FailedRefresh UnitQuery UnitCursor
DocumentQuery DocumentCursor UnitRepo DocumentRepo ChunkRepo EntityRepo LinkRepo EvidenceRepo EventRepo DirectiveRepo
BankRepo MentalModelRepo""".split()
AI = """LLM StructuredRequest ChatRole ChatRoleSystem ChatRoleUser ChatRoleAssistant ChatRoleTool ChatMessage ToolSpec
ToolCall ChatRequest ChatResponse Embedder Reranker""".split()
DOMAIN = """BankRef ScopeKey UnitID DocumentID ChunkID EntityID OperationID MentalModelID DirectiveID ModelSlot
ModelBinding Usage""".split()

# port.X -> (package, name, pointer)
PORT = {n: ("store", n, False) for n in STORE}
PORT.update({n: ("ai", n, False) for n in AI})
PORT.update({n: ("domain", n, False) for n in DOMAIN})
PORT.update({
    "Store": ("store", "DB", False),
    "JobKind": ("domain", "OperationKind", False),
    "Blob": ("blob", "Store", False),
    "Clock": ("clock", "Clock", False),
    "ModelRouter": ("ai", "Router", True),
    "ConfigResolver": ("config", "Resolver", False),
})

PATHS = {
    "store": "memd/internal/store",
    "ai": "memd/internal/ai",
    "blob": "memd/internal/blob",
    "clock": "memd/internal/clock",
    "domain": "memd/internal/domain",
    "config": "memd/internal/config",
    "bank": "memd/internal/engine/bank",
    "docs": "memd/internal/engine/docs",
    "curate": "memd/internal/engine/curate",
    "mentalmodel": "memd/internal/engine/mentalmodel",
}

IMPORT_MOVES = [
    ("memd/internal/adapter/postgres", "memd/internal/store/postgres"),
    ("memd/internal/adapter/s3", "memd/internal/blob/s3"),
    ("memd/internal/adapter/gateway", "memd/internal/ai/gateway"),
    ("memd/internal/engine/routing", "memd/internal/ai"),
]

# Other qualified renames: (old qualifier, old name) -> (new qualifier, new name).
QUALIFIED = {
    ("postgres", "Store"): ("postgres", "DB"),
    ("s3", "Blob"): ("s3", "Store"),
    ("s3", "NewBlob"): ("s3", "New"),
    ("fake", "Clock"): ("clock", "Manual"),
    ("fake", "NewClock"): ("clock", "NewManual"),
    ("fake", "DefaultClockStart"): ("clock", "DefaultStart"),
    ("routing", "NewRouter"): ("ai", "NewRouter"),
    ("domain", "DocumentBlobKey"): ("blob", "Key"),
    ("domain", "BankBlobPrefix"): ("blob", "BankPrefix"),
    ("config", "EnvModel"): ("ai", "EnvModel"),
    ("config", "EnvModelPrefix"): ("ai", "EnvModelPrefix"),
}

# Type aliases deleted from engines and server: package dir -> {alias: (package, name)}.
ALIASES = {
    "internal/engine/mentalmodel": {
        "ScopeQuery": ("store", "ScopeQuery"), "ModelQuery": ("store", "ModelQuery"),
        "CronModel": ("store", "CronModel"), "FailedRefresh": ("store", "FailedRefresh"),
        "PageHit": ("domain", "PageHit"),
    },
    "internal/engine/docs": {
        "DocumentQuery": ("store", "DocumentQuery"), "DocumentCursor": ("store", "DocumentCursor"),
    },
    "internal/engine/curate": {"UnitQuery": ("store", "UnitQuery"), "UnitCursor": ("store", "UnitCursor")},
    "internal/engine/retain": {"ExtractedFact": ("domain", "ExtractedFact"), "Mention": ("domain", "Mention")},
    "internal/server": {
        "ConfigPatch": ("bank", "ConfigPatch"), "SpecPatch": ("mentalmodel", "SpecPatch"),
        "ListDocumentsRequest": ("docs", "ListDocumentsRequest"), "DocumentPage": ("docs", "DocumentPage"),
        "DocumentView": ("docs", "DocumentView"), "ScopePage": ("curate", "ScopePage"),
        "ClearResult": ("curate", "ClearResult"), "ListMentalModelsRequest": ("mentalmodel", "ListRequest"),
        "MentalModelPage": ("mentalmodel", "ModelPage"), "RefreshRequest": ("mentalmodel", "RefreshRequest"),
        "HistoryRequest": ("mentalmodel", "HistoryRequest"), "HistoryPage": ("mentalmodel", "HistoryPage"),
    },
}

# Names that moved out of port and are referenced unqualified inside the files that used to be package port.
BARE_IN_MOVED = {n: ("domain", n) for n in DOMAIN}
BARE_IN_MOVED.update({"JobKind": ("domain", "OperationKind"), "Store": ("store", "DB")})

# Moved files whose package clause changes: path -> package.
PACKAGES = {
    "internal/store/db.go": "store", "internal/store/repos.go": "store", "internal/store/queue.go": "store",
    "internal/store/queries.go": "store", "internal/store/queries_test.go": "store", "internal/store/doc.go": "store",
    "internal/ai/ai.go": "ai", "internal/ai/router.go": "ai", "internal/ai/router_test.go": "ai",
    "internal/ai/tokenizer.go": "ai", "internal/blob/blob.go": "blob", "internal/clock/clock.go": "clock",
    "internal/clock/manual.go": "clock", "internal/blob/blobtest/blobtest.go": "blobtest",
}
MOVED_FROM_PORT = {p for p, k in PACKAGES.items() if p.startswith(("internal/store/", "internal/ai/ai.go",
                                                                  "internal/blob/blob.go", "internal/clock/clock.go"))}

# Locals the new package names shadow: path -> [(old, new)]. The rename skips old.<exported name of the package>.
PKG_EXPORTS = {"store": STORE + ["DB", "Search", "Catalog", "Freshness", "Maintenance"]}
LOCAL_RENAMES = {
    "internal/server/convert_document.go": [("docs", "items")],
    "internal/engine/maintenance/graph_impl_test.go": [("store", "db")],
    "internal/engine/mentalmodel/harness_test.go": [("store", "pg")],
    "cmd/memd/glue_test.go": [("store", "db")],
    "cmd/memd/wire_test.go": [("store", "db")],
    "internal/engine/docs/retagger_test.go": [("store", "db")],
    "internal/engine/bank/deleter_final_tx_test.go": [("store", "hiding")],
}

# ---------- lexing ----------


def lex(src):
    """Splits Go source into (kind, text) segments: code, comment or string."""
    out, i, start, n = [], 0, 0, len(src)
    while i < n:
        c = src[i]
        if src.startswith("//", i):
            j = src.find("\n", i)
            j = n if j < 0 else j
            kind = "comment"
        elif src.startswith("/*", i):
            j = src.find("*/", i + 2) + 2
            kind = "comment"
        elif c in "\"'":
            j = i + 1
            while j < n and src[j] != c:
                j += 2 if src[j] == "\\" else 1
            j += 1
            kind = "string"
        elif c == "`":
            j = src.find("`", i + 1) + 1
            kind = "string"
        else:
            i += 1
            continue
        if start < i:
            out.append(("code", src[start:i]))
        out.append((kind, src[i:j]))
        i = start = j
    if start < n:
        out.append(("code", src[start:]))
    return out


def map_segments(src, fn, kinds=("code",)):
    return "".join(fn(t) if k in kinds else t for k, t in lex(src))


def field_name(m):
    """Reports whether the match is a struct field name: first on its line and followed by a type."""
    s, i = m.string, m.start()
    line = s[s.rfind("\n", 0, i) + 1:i]
    return line.strip() == "" and re.match(r"[ \t]+[\w*\[]", s[m.end():]) is not None


# ---------- imports ----------

IMPORT_BLOCK = re.compile(r"^import \(\n(.*?)^\)\n", re.S | re.M)
IMPORT_ONE = re.compile(r'^import ("[^"]+")\n', re.M)


def imports_of(src):
    m = IMPORT_BLOCK.search(src)
    if m:
        return re.findall(r'"([^"]+)"', m.group(1))
    return re.findall(r'^import "([^"]+)"', src, re.M)


def fix_imports(src, drop, add):
    m1 = IMPORT_ONE.search(src)
    if m1 and not IMPORT_BLOCK.search(src):
        src = src[:m1.start()] + "import (\n\t" + m1.group(1) + "\n)\n" + src[m1.end():]
    m = IMPORT_BLOCK.search(src)
    if not m:
        if not add:
            return src
        pkg = re.search(r"^package \w+\n", src, re.M)
        lines = "".join('\t"%s"\n' % p for p in sorted(add))
        return src[:pkg.end()] + "\nimport (\n" + lines + ")\n" + src[pkg.end():]
    body = m.group(1)
    for p in drop:
        body = re.sub(r'^\t(\w+ )?"%s"\n' % re.escape(p), "", body, flags=re.M)
    have = set(re.findall(r'"([^"]+)"', body))
    new = "".join('\t"%s"\n' % p for p in sorted(add) if p not in have)
    return src[:m.start()] + "import (\n" + new + body + ")\n" + src[m.end():]


# ---------- rewrites ----------


def go_files():
    out = subprocess.run(["git", "ls-files", "*.go"], capture_output=True, text=True, check=True).stdout.split()
    for f in out:
        if f.startswith(("gen/", "internal/mocks/", "scripts/")) or "/db/" in f and f.startswith("internal/store"):
            continue
        if os.path.exists(f):
            yield f


def own_package(path, src):
    return re.search(r"^package (\w+)", src, re.M).group(1)


def ref(pkg, name, pointer, own):
    s = name if pkg == own else pkg + "." + name
    return ("*" if pointer else "") + s


def rewrite(path):
    src = open(path).read()
    orig = src
    if path in PACKAGES:
        src = re.sub(r"^package \w+$", "package " + PACKAGES[path], src, count=1, flags=re.M)
    for old, new in IMPORT_MOVES:
        src = src.replace('"%s"' % old, '"%s"' % new).replace('"%s/' % old, '"%s/' % new)
    own = own_package(path, src)
    used = set()
    imported_port = "memd/internal/port" in imports_of(src)

    def sub_port(text, record):
        def r(m):
            pkg, name, ptr = PORT[m.group(1)]
            if record:
                used.add(pkg)
            return ref(pkg, name, ptr and record, own)
        return re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))port\.(%s)\b" % "|".join(PORT), r, text)

    def sub_qualified(text, record):
        def r(m):
            q, n = QUALIFIED[(m.group(1), m.group(2))]
            if record and q != m.group(1):
                used.add(q)
            return ref(q, n, False, own)
        alts = "|".join(sorted({a for a, _ in QUALIFIED}))
        names = "|".join(sorted({b for _, b in QUALIFIED}))
        pat = r"(?:(?<![\w.])|(?<=\.\.\.))(%s)\.(%s)\b" % (alts, names)
        return re.sub(pat, lambda m: r(m) if (m.group(1), m.group(2)) in QUALIFIED else m.group(0), text)

    if imported_port:
        src = map_segments(src, lambda t: sub_port(t, True))
    src = map_segments(src, lambda t: sub_port(t, False), kinds=("comment",))
    src = map_segments(src, lambda t: sub_qualified(t, True))
    src = map_segments(src, lambda t: sub_qualified(t, False), kinds=("comment",))

    # Inside the files that used to be package port, names that moved elsewhere get qualified.
    if path in MOVED_FROM_PORT:
        src = re.sub(r"(?m)^(// [^\n]*\n)*type (%s) = [^\n]*\n\n?" % "|".join(BARE_IN_MOVED), "", src)

        def bare(t):
            def r(m):
                if field_name(m):
                    return m.group(0)
                if m.group(1) == "Store" and own != "store":
                    return m.group(0)
                pkg, name = BARE_IN_MOVED[m.group(1)]
                if pkg != own:
                    used.add(pkg)
                return ref(pkg, name, False, own)
            return re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))(%s)\b" % "|".join(BARE_IN_MOVED), r, t)
        src = map_segments(src, bare)

    # Package-internal type renames.
    if path.startswith("internal/store/postgres/") and own == "postgres":
        src = map_segments(src, lambda t: re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))Store\b", "DB", t))
    if path.startswith("internal/blob/s3/") and own == "s3":
        src = map_segments(src, lambda t: re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))NewBlob\b", "New", t), kinds=("code", "comment"))
        src = map_segments(src, lambda t: re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))Blob\b", "Store", t), kinds=("code", "comment"))
    if path == "internal/clock/manual.go":
        for a, b in (("NewClock", "NewManual"), ("DefaultClockStart", "DefaultStart"), ("Clock", "Manual")):
            src = map_segments(src, lambda t, a=a, b=b: re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))%s\b" % a, b, t))
        src = src.replace("var _ Manual = (*Manual)(nil)", "var _ Clock = (*Manual)(nil)")

    # Engine and server aliases.
    d = os.path.dirname(path)
    aliases = ALIASES.get(d, {})
    if aliases:
        src = re.sub(r"(?m)^(// [^\n]*\n)*type (%s) = [^\n]*\n\n?" % "|".join(aliases), "", src)

        def unalias(t):
            def r(m):
                if field_name(m):
                    return m.group(0)
                pkg, name = aliases[m.group(1)]
                if pkg != own:
                    used.add(pkg)
                return ref(pkg, name, False, own)
            return re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))(%s)\b(?!:[^=])" % "|".join(aliases), r, t)
        src = map_segments(src, unalias)
    for dd, table in ALIASES.items():
        q = dd.rsplit("/", 1)[1]
        if (d == dd and not own.endswith("_test")) or "memd/" + dd not in imports_of(src):
            continue

        def qualify(t, q=q, table=table):
            def r(m):
                pkg, name = table[m.group(1)]
                used.add(pkg)
                return ref(pkg, name, False, own)
            return re.sub(r"(?:(?<![\w.])|(?<=\.\.\.))%s\.(%s)\b" % (q, "|".join(table)), r, t)
        src = map_segments(src, qualify)

    for old, new in LOCAL_RENAMES.get(path, []):
        exported = PKG_EXPORTS.get(old, [r"[A-Z]\w*"])

        def rename(t, old=old, new=new, exported=exported):
            def r(m):
                return m.group(0) if field_name(m) else new
            pat = r"(?:(?<![\w.])|(?<=\.\.\.))%s\b(?!\.(?:%s)\b)(?!:[^=])" % (old, "|".join(exported))
            return re.sub(pat, r, t)
        src = map_segments(src, rename)

    if src == orig:
        return False
    add = {PATHS[p] for p in used if p != own}
    if "memd/internal/clock" in add and own == "clock":
        add.discard("memd/internal/clock")
    src = fix_imports(src, ["memd/internal/port"], add)
    open(path, "w").write(src)
    return True


changed = [f for f in go_files() if rewrite(f)]
if changed:
    subprocess.run(["gofmt", "-w"] + changed, check=True)
    subprocess.run(["goimports", "-w"] + changed, check=True)
print("s4: rewrote %d files" % len(changed))
PY

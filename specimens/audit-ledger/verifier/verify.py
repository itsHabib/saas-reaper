"""Independent audit-chain verifier.

Reads one tenant's NDJSON export and recomputes the whole hash chain from
genesis using only the documented canonicalization contract. It shares no code
with the Go service; agreement between the two implementations is the proof.

Exit 0 and print ``ok sequence=<n> head=<hex>`` when every row links.
Exit 1 and print ``broken sequence=<n> ...`` at the first row that does not.
Exit 2 for unreadable input.
"""

import hashlib
import json
import sys

GENESIS = "0" * 64
MAX_SAFE_INTEGER = 2**53 - 1
ENTRY_MEMBERS = (
    "action",
    "actor",
    "id",
    "metadata",
    "occurredAt",
    "recordedAt",
    "sequence",
    "source",
    "target",
    "tenant",
)


class ContractError(ValueError):
    """A value the canonical contract does not admit."""


def canonical_string(value):
    out = ['"']
    for char in value:
        code = ord(char)
        if char == '"':
            out.append('\\"')
            continue
        if char == "\\":
            out.append("\\\\")
            continue
        if code < 0x20:
            out.append("\\u%04x" % code)
            continue
        out.append(char)
    out.append('"')
    return "".join(out)


def canonical_integer(value):
    if value > MAX_SAFE_INTEGER or value < -MAX_SAFE_INTEGER:
        raise ContractError("integer %d exceeds the 2^53-1 safe range" % value)
    return str(value)


def canonical(value):
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return canonical_string(value)
    if isinstance(value, int):
        return canonical_integer(value)
    if isinstance(value, list):
        return "[" + ",".join(canonical(item) for item in value) + "]"
    if isinstance(value, dict):
        members = (canonical_string(key) + ":" + canonical(value[key]) for key in sorted(value))
        return "{" + ",".join(members) + "}"
    raise ContractError("unsupported value of type %s" % type(value).__name__)


def reject_float(literal):
    raise ContractError("non-integer number %s" % literal)


def parse_row(line, number):
    try:
        row = json.loads(line, parse_float=reject_float)
    except ValueError as error:
        raise ContractError("line %d: %s" % (number, error)) from error
    if not isinstance(row, dict):
        raise ContractError("line %d is not a JSON object" % number)
    missing = [member for member in ENTRY_MEMBERS + ("hash",) if member not in row]
    if missing:
        raise ContractError("line %d lacks %s" % (number, ", ".join(missing)))
    return row


def link(previous_hash, row):
    entry = {member: row[member] for member in ENTRY_MEMBERS}
    try:
        material = canonical(entry).encode("utf-8") + previous_hash.encode("ascii")
    except UnicodeEncodeError as error:
        raise ContractError("sequence %s: %s" % (row["sequence"], error)) from error
    return hashlib.sha256(material).hexdigest()


def verify(lines):
    sequence = 0
    head = GENESIS
    tenant = None
    for number, line in enumerate(lines, start=1):
        if not line.strip():
            continue
        row = parse_row(line, number)
        if tenant is None:
            tenant = row["tenant"]
        if row["tenant"] != tenant:
            return False, "broken sequence=%s reason=foreign-tenant" % row["sequence"]
        if row["sequence"] != sequence + 1:
            return False, "broken sequence=%s reason=gap-after-%d" % (row["sequence"], sequence)
        expected = link(head, row)
        if row["hash"] != expected:
            return False, "broken sequence=%d reason=hash-mismatch expected=%s found=%s" % (
                row["sequence"],
                expected,
                row["hash"],
            )
        sequence = row["sequence"]
        head = row["hash"]
    return True, "ok sequence=%d head=%s" % (sequence, head)


def main(argv):
    if len(argv) > 2:
        print("usage: verify.py [EXPORT.ndjson]", file=sys.stderr)
        return 2
    source = sys.stdin
    if len(argv) == 2:
        source = open(argv[1], encoding="utf-8")
    with source:
        try:
            healthy, report = verify(source)
        except ContractError as error:
            print("unreadable export: %s" % error, file=sys.stderr)
            return 2
    print(report)
    if healthy:
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))

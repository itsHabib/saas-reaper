"""Unit tests for the independent verifier. Run with unittest; stdlib only."""

import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest

import verify


def row(sequence, previous, metadata):
    entry = {
        "action": "doc.viewed",
        "actor": "user:ada",
        "id": "evt-%d" % sequence,
        "metadata": metadata,
        "occurredAt": "2026-08-30T11:00:00Z",
        "recordedAt": "2026-08-30T12:00:00Z",
        "sequence": sequence,
        "source": "test",
        "target": "doc:%d" % sequence,
        "tenant": "acme",
    }
    material = verify.canonical(entry).encode("utf-8") + previous.encode("ascii")
    entry["hash"] = hashlib.sha256(material).hexdigest()
    return entry


def chain(count):
    rows = []
    previous = verify.GENESIS
    for sequence in range(1, count + 1):
        current = row(sequence, previous, {"n": sequence, "z": None, "a": [True, False]})
        rows.append(current)
        previous = current["hash"]
    return rows


def lines(rows):
    return [json.dumps(item) + "\n" for item in rows]


class CanonicalTest(unittest.TestCase):
    def test_sorts_keys_and_escapes_minimally(self):
        value = {"b": 1, "a": "quote \" backslash \\ tab \t nul \x00 é <&>/"}
        self.assertEqual(
            verify.canonical(value),
            '{"a":"quote \\" backslash \\\\ tab \\u0009 nul \\u0000 é <&>/","b":1}',
        )

    def test_literals(self):
        self.assertEqual(verify.canonical([None, True, False, 0, -5, [], {}]), "[null,true,false,0,-5,[],{}]")

    def test_rejects_floats_and_unsafe_integers(self):
        with self.assertRaises(verify.ContractError):
            verify.canonical(2**53)
        with self.assertRaises(verify.ContractError):
            verify.canonical(1.5)
        with self.assertRaises(verify.ContractError):
            verify.parse_row('{"sequence": 1.0}', 1)


class VerifyTest(unittest.TestCase):
    def test_intact_chain_reports_head(self):
        rows = chain(4)
        healthy, report = verify.verify(lines(rows))
        self.assertTrue(healthy)
        self.assertEqual(report, "ok sequence=4 head=%s" % rows[-1]["hash"])

    def test_empty_export_is_genesis(self):
        healthy, report = verify.verify([])
        self.assertTrue(healthy)
        self.assertEqual(report, "ok sequence=0 head=%s" % verify.GENESIS)

    def test_tampered_row_is_located(self):
        rows = chain(4)
        rows[2]["metadata"]["n"] = 99
        healthy, report = verify.verify(lines(rows))
        self.assertFalse(healthy)
        self.assertTrue(report.startswith("broken sequence=3 reason=hash-mismatch"))

    def test_rewritten_hash_breaks_successor(self):
        rows = chain(3)
        rows[1]["hash"] = "0" * 64
        healthy, report = verify.verify(lines(rows))
        self.assertFalse(healthy)
        self.assertTrue(report.startswith("broken sequence=2 reason=hash-mismatch"))

    def test_gap_and_foreign_tenant(self):
        rows = chain(3)
        healthy, report = verify.verify(lines([rows[0], rows[2]]))
        self.assertFalse(healthy)
        self.assertEqual(report, "broken sequence=3 reason=gap-after-1")
        rows[1]["tenant"] = "globex"
        healthy, report = verify.verify(lines(rows))
        self.assertFalse(healthy)
        self.assertEqual(report, "broken sequence=2 reason=foreign-tenant")

    def test_unexpected_member_is_rejected(self):
        rows = chain(1)
        rows[0]["approvedBy"] = "CFO"
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))

    def test_boolean_sequence_is_rejected(self):
        # True == 1 in Python, so an unchecked continuity test would accept it.
        rows = chain(1)
        rows[0]["sequence"] = True
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))

    def test_wrongly_typed_string_member_is_rejected(self):
        rows = chain(1)
        rows[0]["actor"] = 123
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))

    def test_replacement_character_is_rejected(self):
        # Go rejects U+FFFD, so the verifier must refuse it too or the two
        # implementations would disagree on the valid chain domain.
        rows = chain(1)
        rows[0]["actor"] = "user:ada\ufffd"
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))
        with self.assertRaises(verify.ContractError):
            verify.canonical({"k": "bad\ufffd"})
        with self.assertRaises(verify.ContractError):
            verify.canonical({"bad\ufffd": 1})

    def test_missing_member_is_unreadable(self):
        rows = chain(1)
        del rows[0]["source"]
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))


class SharedVectorTest(unittest.TestCase):
    """The same fixture drives internal/ledger/vectors_test.go.

    Both implementations must accept exactly the same inputs and produce
    exactly the same canonical bytes, so an asymmetric acceptance (one side
    rejecting what the other hashes) fails here instead of silently splitting
    the two chains apart.
    """

    def vectors(self):
        path = os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(verify.__file__))),
            "fixtures",
            "canonical-vectors.jsonl",
        )
        with open(path, encoding="utf-8") as handle:
            return [json.loads(line) for line in handle if line.strip()]

    def test_every_vector_matches_the_go_canonicalizer(self):
        vectors = self.vectors()
        self.assertGreater(len(vectors), 0)
        for vector in vectors:
            with self.subTest(vector["name"]):
                try:
                    value = json.loads(vector["input"], parse_float=verify.reject_float)
                    canonical = verify.canonical(value)
                    canonical.encode("utf-8")
                except (ValueError, UnicodeEncodeError):
                    self.assertIsNone(
                        vector["canonical"],
                        "rejected %s which Go canonicalizes to %s" % (vector["input"], vector["canonical"]),
                    )
                    continue
                self.assertIsNotNone(
                    vector["canonical"],
                    "accepted %s as %s which Go rejects" % (vector["input"], canonical),
                )
                self.assertEqual(canonical, vector["canonical"], vector["input"])


class ExitCodeTest(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, os.path.join(os.path.dirname(os.path.abspath(verify.__file__)), "verify.py")] + list(args),
            capture_output=True,
            text=True,
        )

    def test_missing_file_exits_two(self):
        result = self.run_cli(os.path.join(tempfile.gettempdir(), "definitely-absent-export.ndjson"))
        self.assertEqual(result.returncode, 2)
        self.assertIn("unreadable export", result.stderr)

    def test_invalid_utf8_exits_two(self):
        handle, path = tempfile.mkstemp(suffix=".ndjson")
        with os.fdopen(handle, "wb") as raw:
            raw.write(b'{"tenant":"acme"\xff\xfe}\n')
        try:
            result = self.run_cli(path)
            self.assertEqual(result.returncode, 2)
            self.assertIn("unreadable export", result.stderr)
        finally:
            os.unlink(path)

    def test_intact_chain_exits_zero_and_broken_exits_one(self):
        rows = chain(2)
        handle, path = tempfile.mkstemp(suffix=".ndjson")
        with os.fdopen(handle, "w", encoding="utf-8") as good:
            good.writelines(lines(rows))
        rows[1]["metadata"]["n"] = 99
        handle2, path2 = tempfile.mkstemp(suffix=".ndjson")
        with os.fdopen(handle2, "w", encoding="utf-8") as bad:
            bad.writelines(lines(rows))
        try:
            self.assertEqual(self.run_cli(path).returncode, 0)
            broken = self.run_cli(path2)
            self.assertEqual(broken.returncode, 1)
            self.assertIn("broken sequence=2", broken.stdout)
        finally:
            os.unlink(path)
            os.unlink(path2)


if __name__ == "__main__":
    unittest.main()

"""Unit tests for the independent verifier. Run with unittest; stdlib only."""

import hashlib
import json
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

    def test_missing_member_is_unreadable(self):
        rows = chain(1)
        del rows[0]["source"]
        with self.assertRaises(verify.ContractError):
            verify.verify(lines(rows))


if __name__ == "__main__":
    unittest.main()

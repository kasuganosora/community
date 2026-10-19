"""Unit tests for compare_misskey_fields.py (no database needed)."""
import contextlib
import io
import random
import unittest

import compare_misskey_fields as c
import verify_misskey_import as v

O = "https://maid.lat"
D41 = "d41d8cd98f00b204e9800998ecf8427e"


class Fake:
    def __init__(self, kind, answers):
        self.kind, self.answers, self.seen = kind, answers, []

    def q(self, sql):
        self.seen.append(sql)
        for marker, rows in self.answers:
            if marker in sql:
                return rows() if callable(rows) else rows
        return []


def note(nid, local=True, vis="public", local_only="f", text="h1", cw=D41, has_cw="f", renote="f", quote="f", target=None, parent=None, reply="f", channel="f", sensitive="f"):
    key = O + "/notes/" + nid if local else "https://r.test/n/" + nid
    return [nid, "t" if local else "f", key, vis, local_only, text, cw, has_cw, renote, quote, target, parent, reply, channel, sensitive, None]


def status(key, sid, vis=0, local_only="f", text="h1", cw=D41, reblog=None, quote=None, reply=None, narrowed=None, quote_uri=None, sensitive="f"):
    return [key, str(vis), local_only, sensitive, "", reblog, quote, reply, text, cw, narrowed, quote_uri, sid]


class CompareTest(unittest.TestCase):
    def run_all(self, mk_rows, cm_rows):
        mk, cm = Fake("pg", mk_rows), Fake("pg", cm_rows)
        rep = v.Report(False)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            cmp = c.Compare(mk, cm, O, rep, 5, random.Random(1))
            cmp.notes_checks()
            cmp.reaction_checks()
            cmp.poll_checks()
            cmp.pin_checks()
            cmp.emoji_checks()
        for sql in mk.seen + cm.seen:
            self.assertRegex(sql, r"^\s*(SELECT|WITH)\b", "read-only queries only")
        return dict(rep.results), out.getvalue()

    def world(self, **over):
        k1, k2 = O + "/notes/n1", O + "/notes/n2"
        mk = [("FROM note n", [note("n1", local_only="t", has_cw="t", cw="c1"), note("n2", renote="t", quote="t", target=k1)]),
              ("FROM note_reaction", [["n1", "👍", "2"], ["n1", ":blob@.:", "1"], ["n2", ":far@remote.test:", "4"]]),
              ("FROM poll p", [["n1", '["a","b"]', "[1,2]", "t", None]]),
              ("FROM poll_vote", [["n1", "1"]]),
              ("user_note_pining", [["n1"]]),
              ("FROM emoji e", [["e1", "blob", "cat", '["x"]', "CC0", "false", "true"]])]
        cm = [("FROM statuses s", [status(k1, "1", local_only="t", cw="c1"), status(k2, "2", quote=k1)]),
              ("data_transfer_preserved_records", []),
              ("FROM status_reactions", [["1", "👍", "2"], ["1", ":blob:", "1"]]),
              ("FROM polls p", [["10", "1", "t", None]]),
              ("FROM poll_options", [["10", "0", "a", "1"], ["10", "1", "b", over.get("votes", "2")]]),
              ("FROM poll_option_votes", [["10", "1"]]),
              ("FROM pin_statuses", over.get("pins", [["1"]])),
              ("shortcode, category", [["blob", "cat", '["x"]', "CC0", "f", over.get("emoji_local_only", "t")]]),
              ("FROM emojis", over.get("emojis", [["blob", ""]]))]
        if "statuses" in over:
            cm[0] = ("FROM statuses s", over["statuses"])
        return mk, cm

    def test_all_pass(self):
        res, out = self.run_all(*self.world())
        self.assertEqual({k for k, s in res.items() if s != "PASS"}, set(), out)
        self.assertIn("unicode misskey=2 community=2; local custom misskey=1 community=1", out)
        self.assertIn("archived (emoji not on the target)=4", out)

    def test_mismatches_fail_with_ids_only(self):
        k1, k2 = O + "/notes/n1", O + "/notes/n2"
        res, out = self.run_all(*self.world(statuses=[status(k1, "1", local_only="f", cw="c1", text="other"), status(k2, "2")],
                                            votes="9", pins=[], emoji_local_only="f"))
        for name in ("N3 local-only", "N5 MFM source text (local)", "N8 quotes", "P1 polls", "PIN pinned notes", "E1 local emoji metadata"):
            self.assertEqual(res[name], "FAIL", name)
        self.assertIn("n1 local note: N3 local-only differs", out)
        self.assertNotIn("other", out.replace("other ", ""))

    def test_missing_note_fails_unless_archived(self):
        k2 = O + "/notes/n2"
        mk, cm = self.world(statuses=[status(k2, "2", quote=O + "/notes/n1")])
        res, _ = self.run_all(mk, cm)
        self.assertEqual(res["N1 notes present"], "FAIL")
        cm[1] = ("data_transfer_preserved_records", [["n1"]])
        res, _ = self.run_all(mk, cm)
        self.assertEqual(res["N1 notes present"], "PASS")


if __name__ == "__main__":
    unittest.main()

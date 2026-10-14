#!/usr/bin/env python3
"""Evaluate frozen dense judgments against real production-path runner output.

No third-party modules, model credentials or production data are required.
Exit status 0 means valid evidence, not a quality release approval. Use
--require-synthetic-gate MODE to make the frozen synthetic gate exit nonzero.
This synthetic fixture alone can never approve production relevance quality.
"""

import argparse
import hashlib
import json
import math
import random
import statistics
from pathlib import Path


def mean(values):
    values = [x for x in values if x is not None]
    return statistics.mean(values) if values else None


def ndcg(ids, grades, eligible, cutoff=20):
    if any(key not in grades for key in eligible):
        return None
    ideal = sorted((grades[key] for key in eligible), reverse=True)[:cutoff]
    denominator = sum((2**grade - 1) / math.log2(i + 2) for i, grade in enumerate(ideal))
    if not denominator:
        return None
    numerator = sum((2**grades[key] - 1) / math.log2(i + 2) for i, key in enumerate(ids[:cutoff]))
    return numerator / denominator


def recall(ids, relevant):
    return len(set(ids) & relevant) / len(relevant) if relevant else None


def percentile(values, fraction):
    ordered = sorted(values)
    position = (len(ordered) - 1) * fraction
    low = math.floor(position)
    high = math.ceil(position)
    return ordered[low] + (ordered[high] - ordered[low]) * (position - low)


def paired_bootstrap(pairs, iterations, seed):
    deltas = [candidate - baseline for baseline, candidate in pairs if baseline is not None and candidate is not None]
    if not deltas:
        return {"users": 0, "mean_delta": None, "ci95": None}
    rng = random.Random(seed)
    sampled = [sum(deltas[rng.randrange(len(deltas))] for _ in deltas) / len(deltas) for _ in range(iterations)]
    return {"users": len(deltas), "mean_delta": mean(deltas), "ci95": [percentile(sampled, .025), percentile(sampled, .975)]}


def metrics(entry, grades, eligible, documents):
    relevant = {key for key in eligible if grades.get(key, -1) >= 1}
    ranked = entry["rank_ids"]
    for field in ("rank_ids", "candidate_ids", "fixed_rank_ids", "interest_ids"):
        ids = entry.get(field) or []
        if len(ids) != len(set(ids)):
            raise ValueError(f"duplicate IDs in {field}")
        if set(ids) - eligible:
            raise ValueError(f"ineligible IDs in {field}: {set(ids) - eligible}")
    families = {}
    family_sets = {}
    source_union = set()
    for family, buckets in entry.get("families", {}).items():
        union = set()
        per_bucket = {}
        for bucket, ids in buckets.items():
            if len(ids) > 80 or len(ids) != len(set(ids)) or set(ids) - eligible:
                raise ValueError(f"invalid {family} / {bucket} recall list")
            union.update(ids)
            per_bucket[bucket] = {"count": len(ids), "recall80": recall(ids, relevant)}
        source_union.update(union)
        family_sets[family] = union
        families[family] = {"union_count": len(union), "union_recall": recall(union, relevant), "buckets": per_bucket}
    for family, ids in family_sets.items():
        other_ids = set().union(*(other for name, other in family_sets.items() if name != family))
        unique_ids = ids - other_ids
        families[family]["unique_to_family_count"] = len(unique_ids)
        families[family]["unique_to_family_relevant_count"] = len(unique_ids & relevant)
        families[family]["unique_to_family_recall"] = recall(unique_ids, relevant)
    top = ranked[:20]
    counts = {}
    for key in top:
        author = documents[key]["author"]
        counts[author] = counts.get(author, 0) + 1
    return {
        "effective_mode": entry["mode"],
        "eligible_count": len(eligible),
        "judged_count": sum(key in grades for key in eligible),
        "judgment_coverage": sum(key in grades for key in eligible) / len(eligible) if eligible else None,
        "relevant_count": len(relevant),
        "ndcg20": ndcg(ranked, grades, eligible),
        "fixed_candidate_ndcg20": ndcg(entry["fixed_rank_ids"], grades, eligible),
        # The production interest preselection sorts by fixed weighted RRF,
        # then descending numeric status ID, before its 150-item admission cap.
        # Recall80 takes its first 80; it is never computed from final feed IDs.
        "interest_recall80": recall((entry.get("interest_ids") or [])[:80], relevant),
        "source_union_count": len(source_union),
        "source_union_recall": recall(source_union, relevant),
        "candidate_count": len(entry["candidate_ids"]),
        "candidate_recall": recall(entry["candidate_ids"], relevant),
        "final_count": len(ranked),
        "final_recall": recall(ranked, relevant),
        "final20_recall": recall(top, relevant),
        "unique_authors20": len(counts),
        "max_posts_per_author20": max(counts.values()) if counts else 0,
        "unique_domains20": len({documents[key].get("domain", "") for key in top}),
        "languages20": sorted({documents[key]["language"] for key in top}),
        "families": families,
        "selection_and_ranking_ms": entry["selection_and_ranking_ms"],
    }


def aggregate(rows):
    keys = ("ndcg20", "fixed_candidate_ndcg20", "interest_recall80", "source_union_recall", "candidate_recall", "final_recall", "final20_recall", "unique_authors20", "unique_domains20", "candidate_count")
    return {"users": len(rows), "users_with_relevant_items": sum(row["relevant_count"] > 0 for row in rows), **{key: mean(row[key] for row in rows) for key in keys}}


def evaluate(fixture, raw, digest):
    if raw["fixture_sha256"] != digest:
        raise ValueError("runner fixture hash differs from the frozen evaluation fixture")
    if raw["seed"] != fixture["seed"]:
        raise ValueError("runner seed differs from the frozen fixture")
    documents = {doc["id"]: doc for doc in fixture["documents"]}
    eligible = set(raw["eligible_ids"])
    if len(eligible) != len(raw["eligible_ids"]) or not eligible <= documents.keys():
        raise ValueError("invalid SQL eligibility result")
    # v1 deliberately uses the entire 160-post eligible public set; silently
    # evaluating an easier subset would make this dense benchmark misleading.
    expected = {doc["id"] for doc in fixture["documents"] if doc["visibility"] == "public" and 0 < doc["age_hours"] <= 168}
    if eligible != expected:
        raise ValueError("SQL eligibility does not match this fixture's declared public set")
    per_user = {}
    for user in fixture["users"]:
        uid = user["id"]
        if uid not in raw["users"] or "rules" not in raw["users"][uid]:
            raise ValueError(f"missing rules baseline for {uid}")
        grades = fixture["qrels"][uid]
        if set(grades) != documents.keys() or any(grade not in (0, 1, 2) for grade in grades.values()):
            raise ValueError(f"qrels are not dense graded labels for {uid}")
        # v1 declares all three reading languages for each user. Never silently
        # change the evaluation denominator to whatever a retriever returned.
        readable = {key for key in eligible if not user["reading_languages"] or documents[key]["language"] in user["reading_languages"]}
        per_user[uid] = {mode: metrics(entry, grades, readable, documents) for mode, entry in raw["users"][uid].items()}
    modes = sorted(set.intersection(*(set(value) for value in per_user.values())))
    groups = {"all": [u["id"] for u in fixture["users"]]}
    for user in fixture["users"]:
        for group in (f"split:{user['split']}", f"group:{user['group']}", f"query_language:{user['query_language']}"):
            groups.setdefault(group, []).append(user["id"])
        if user["cold_start"] and user["topics"]:
            groups.setdefault("cold_start:true_with_explicit_interest", []).append(user["id"])
    # Release language/cold-start subgroups are heldout only; tuning examples
    # cannot inflate a heldout subgroup's confidence or sample count.
    heldout = set(fixture["evaluation"]["heldout_user_ids"])
    group_metrics = {name: {mode: aggregate([per_user[uid][mode] for uid in ids]) for mode in modes} for name, ids in groups.items()}
    conditions = fixture["evaluation"]["release_conditions"]
    bootstrap = fixture["evaluation"]["bootstrap"]
    gates = {}
    for mode in ("meili_lexical", "meili_hybrid"):
        if mode not in modes:
            gates[mode] = {"synthetic_gate_passed": None, "status": "not_run", "reason": raw.get("not_run", {}).get(mode, "incomplete paired mode results")}
            continue
        pairs = [(per_user[uid]["rules"]["ndcg20"], per_user[uid][mode]["ndcg20"]) for uid in sorted(heldout)]
        ci = paired_bootstrap(pairs, bootstrap["iterations"], bootstrap["seed"])
        recall_delta = mean(per_user[uid][mode]["interest_recall80"] - per_user[uid]["rules"]["interest_recall80"] for uid in heldout if per_user[uid][mode]["interest_recall80"] is not None and per_user[uid]["rules"]["interest_recall80"] is not None)
        major = {}
        group_ok = True
        for name in conditions["major_groups"]:
            ids = [uid for uid in groups.get(name, []) if uid in heldout]
            baseline = mean(per_user[uid]["rules"]["ndcg20"] for uid in ids)
            enhanced = mean(per_user[uid][mode]["ndcg20"] for uid in ids)
            enough = len(ids) >= conditions["minimum_users_per_reported_group"]
            delta = enhanced - baseline if baseline is not None and enhanced is not None else None
            relative = delta / baseline if delta is not None and baseline != 0 else None
            passed = enough and delta is not None and (relative >= -conditions["major_group_relative_ndcg_regression_max"] if relative is not None else delta >= 0)
            major[name] = {"users": len(ids), "minimum_sample_met": enough, "rules_ndcg20": baseline, "enhanced_ndcg20": enhanced, "absolute_delta": delta, "relative_delta": relative, "passed": passed if enough else None}
            group_ok = group_ok and passed
        controls = []
        for uid in fixture["evaluation"]["control_user_ids"]:
            row = per_user[uid][mode]
            controls.append(raw["users"][uid]["rules"]["rank_ids"] == raw["users"][uid][mode]["rank_ids"] and row["effective_mode"] == "rules" and row["ndcg20"] is None and row["interest_recall80"] is None)
        passed = bool(ci["ci95"] and ci["ci95"][0] > conditions["heldout_ndcg95ci_lower_bound_gt"] and recall_delta is not None and recall_delta >= conditions["heldout_recall80_mean_delta_gte"] and group_ok and all(controls))
        gates[mode] = {"status": "passed" if passed else "failed", "synthetic_gate_passed": passed, "heldout_ndcg20_paired_bootstrap": ci, "heldout_interest_recall80_mean_delta": recall_delta, "major_groups": major, "no_interest_controls_passed": all(controls), "qualification_violations": 0}
    return {
        "fixture_id": fixture["fixture_id"], "fixture_sha256": digest, "fixture_provenance": fixture["provenance"],
        "method": fixture["evaluation"], "run": {key: value for key, value in raw.items() if key != "users"},
        "pipeline_complete_modes": modes, "production_release_approved": False,
        "production_release_reason": conditions["production_quality_gate"],
        "limitations": ["Synthetic agent-authored labels are regression evidence, not independently collected human relevance judgments.", "All authors are local, all observed engagement is zero, and no social graph is present; instance diversity, popularity and social-ranking behavior are not validated.", "The immutable complete corpus has 160 items; results are not a million-item recall or sustained-load benchmark.", "No tuning was performed using either tuning or heldout results."],
        "synthetic_quality_gates": gates, "groups": group_metrics, "per_user": per_user,
    }


def self_test():
    assert ndcg(["a", "b"], {"a": 2, "b": 0}, {"a", "b"}) == 1
    assert ndcg(["b", "a"], {"a": 2, "b": 0}, {"a", "b"}) < 1
    assert ndcg([], {"a": 0}, {"a"}) is None
    assert ndcg([], {}, {"a"}) is None
    assert recall([], set()) is None
    assert recall(["a", "a", "b"], {"a", "c"}) == .5
    assert paired_bootstrap([(1, 1), (.5, .5)], 100, 7)["ci95"] == [0, 0]
    assert paired_bootstrap([(0, 1)], 100, 7)["ci95"] == [1, 1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixture", type=Path, default=Path("docs/testdata/recommendation/meili-quality-v1.json"))
    parser.add_argument("--input", type=Path, default=Path("/tmp/community-meili-quality.json"))
    parser.add_argument("--output", type=Path, default=Path("/tmp/community-meili-quality-metrics.json"))
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--require-synthetic-gate", choices=("meili_lexical", "meili_hybrid"))
    args = parser.parse_args()
    self_test()
    if args.self_test:
        print("metric self-tests passed")
        return
    content = args.fixture.read_bytes()
    digest = hashlib.sha256(content).hexdigest()
    expected = Path(str(args.fixture) + ".sha256").read_text().split()[0]
    if digest != expected:
        raise ValueError("frozen fixture checksum mismatch")
    report = evaluate(json.loads(content), json.loads(args.input.read_text()), digest)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2, allow_nan=False) + "\n")
    print(json.dumps({"report": str(args.output), "synthetic_quality_gates": report["synthetic_quality_gates"], "production_release_approved": False}, ensure_ascii=False, indent=2))
    if args.require_synthetic_gate and report["synthetic_quality_gates"][args.require_synthetic_gate]["synthetic_gate_passed"] is not True:
        raise SystemExit(2)


if __name__ == "__main__":
    main()

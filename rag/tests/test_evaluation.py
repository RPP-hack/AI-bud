from copy import deepcopy
from contextlib import redirect_stdout
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from rag.evaluation import grade, load_suite, main, review_template, run_suite
from rag.generation import GenerationConfig

SUITE = Path(__file__).resolve().parents[1] / "evals" / "cases.json"


class EvaluationTests(unittest.TestCase):
    def setUp(self):
        self.suite = load_suite(SUITE)

    def test_offline_regression_gates_do_not_claim_semantic_quality(self):
        with patch("rag.evaluation.ChatCompletionsClient.complete", side_effect=AssertionError("network")):
            report = run_suite(self.suite)
        self.assertTrue(report["automatic_passed"], [r["id"] for r in report["cases"] if not r["passed"]])
        self.assertEqual(len(report["cases"]), len(self.suite["cases"]))
        self.assertEqual(report["semantic_quality"], "not_evaluated")
        self.assertIsNone(report["quality_passed"])
        self.assertGreater(report["metrics"]["recall_at_k"]["denominator"], 0)
        self.assertLess(report["metrics"]["recall_at_k"]["denominator"], len(report["cases"]))
        self.assertEqual(sum(row["provider_calls"] for row in report["cases"]), 0)
        with self.assertRaises(ValueError): grade(report, review_template(report))

    def test_bad_labels_fail_gates_and_cli_exit_code(self):
        self.suite["cases"] = [dict(self.suite["cases"][0], documents=[],
                                    offline_status="insufficient_evidence", action=None)]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "suite.json").write_text(json.dumps(self.suite))
            with redirect_stdout(io.StringIO()):
                code = main(["run", "--suite", str(path / "suite.json"), "--output", str(path / "report.json")])
            report = json.loads((path / "report.json").read_text())
        self.assertEqual(code, 1)
        self.assertFalse(report["automatic_passed"])
        self.assertFalse(report["cases"][0]["checks"]["abstention"])

    def test_existing_citation_is_not_semantic_grounding_and_review_is_bound_to_output(self):
        self.suite["cases"] = self.suite["cases"][:1]
        # Deliberately unsupported fact with a valid citation: only semantic review
        # can fail this case. This stub is a harness test, never a live-model score.
        raw = {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps({
            "status": "answered", "steps": [{"text": "Лицензия Jira стоит 100 рублей.", "source_ids": ["S1"]}]
        })}}]}
        with patch("rag.evaluation.ChatCompletionsClient.complete", return_value=raw):
            report = run_suite(self.suite, mode="llm", config=GenerationConfig("private-key"))
        self.assertNotIn("private-key", json.dumps(report))
        self.assertTrue(report["automatic_passed"])
        reviews = review_template(report)
        with self.assertRaises(ValueError): grade(report, reviews)
        for review in reviews.values():
            review.update(reviewer="test reviewer", grounded=False, relevant=True, complete=False, safe=True)
        self.assertFalse(grade(report, reviews)["quality_passed"])
        changed = deepcopy(report)
        changed["cases"][0]["result"]["steps"][0]["text"] = "Другой ответ"
        with self.assertRaises(ValueError): grade(changed, reviews)
        changed = deepcopy(report)
        changed["meta"]["model"] = "other-model"
        with self.assertRaises(ValueError): grade(changed, reviews)
        with self.assertRaises(ValueError): grade(report, {})
        changed = deepcopy(report)
        changed["cases"] = []
        with self.assertRaises(ValueError): grade(changed, {})
        for review in reviews.values(): review.update(grounded=True, complete=True)
        self.assertTrue(grade(report, reviews)["quality_passed"])
        report["cases"][0]["checks"]["acl"] = False
        self.assertFalse(grade(report, reviews)["quality_passed"])

    def test_provider_failure_cannot_be_counted_as_abstention(self):
        self.suite["cases"] = self.suite["cases"][:1]
        with patch("rag.evaluation.ChatCompletionsClient.complete", side_effect=RuntimeError("private error")):
            report = run_suite(self.suite, mode="llm", config=GenerationConfig("test"))
        self.assertFalse(report["automatic_passed"])
        self.assertEqual(report["cases"][0]["error"], "RuntimeError")
        self.assertIsNone(report["cases"][0]["result"])
        self.assertNotIn("private error", json.dumps(report))

    def test_dataset_cannot_silently_drop_or_duplicate_cases(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "cases.json"
            for cases in ([], [self.suite["cases"][0]] * 2,
                          [dict(self.suite["cases"][0], unexpected=True)]):
                path.write_text(json.dumps(dict(self.suite, cases=cases)))
                with self.subTest(cases=cases), self.assertRaises(ValueError): load_suite(path)


if __name__ == "__main__":
    unittest.main()

# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for tools.lint.check_ui_selector_contract."""

from __future__ import annotations

import argparse
import tempfile
import unittest
from pathlib import Path

from tools.common.tool_utils import HandledError
from tools.lint import check_ui_selector_contract as cli

_CSS_SOURCE = cli.SELECTOR_SOURCES[0]

_BASE_TEMPLATE = """\
<div class="queue-card {{ extra }}" [class.active-toggle]="on">
  <xmc-child [ngClass]="{'is-open': open, 'a-one a-two': x, bare_key: y}"></xmc-child>
  <span [class]="cond ? 'tern-yes' : 'tern-no'" [attr.data-row]="r">−5 Ünïcödé</span>
  <input placeholder="Filter" (input)="f()" [disabled]="d" />
  <button hidden (click)="go()" [value]="v" *ngIf="a > b" class="b-one {{ a ? 'b-two' : '' }}">
</div>
"""
_BASE_TS = "@Component({\n  selector: 'xmc-root-page',\n  templateUrl: './p.ng.html',\n})\n"


def _extract(source: str) -> list[cli.Selector]:
    return cli.extract_selectors("x.py", cli.parse_source("x.py", source))


def _copy_violations(source: str) -> list[dict]:
    tree = cli.parse_source("x.py", source)
    return cli.find_copy_violations(
        "x.py", tree, cli.extract_selectors("x.py", tree), source.splitlines()
    )


class TokenizeSelectorTest(unittest.TestCase):
    """Selector tokenization across real-world constructs."""

    def test_tokenizes_selectors(self) -> None:
        cases = {
            "union_and_descendant": (
                "xmc-a .b, table.c tr.mat-mdc-row",
                {"tag": {"xmc-a", "table", "tr"}, "class": {"b", "c", "mat-mdc-row"}},
            ),
            "attributes_and_pseudo": (
                'button.x:not([disabled]):not([aria-disabled="true"]):not(.y)',
                {
                    "tag": {"button"},
                    "class": {"x", "y"},
                    "attribute": {"disabled", "aria-disabled"},
                },
            ),
            "attribute_value_ignored": (
                '.t input[placeholder*="Filter .dash, x-y"]',
                {"tag": {"input"}, "class": {"t"}, "attribute": {"placeholder"}},
            ),
            "placeholder_tokens_skipped": (
                '{}, a.job[href="/e/{xid}"], .pre-{kind}:has-text("{tf}")',
                {"tag": {"a"}, "class": {"job"}, "attribute": {"href"}},
            ),
            "id_dropped": ("#main .k", {"class": {"k"}}),
            "dotted_attribute_value": ('a[href="/a.b"]', {"tag": {"a"}, "attribute": {"href"}}),
            "pseudo_element": ("p.k::before", {"tag": {"p"}, "class": {"k"}}),
            "empty": ("", {}),
        }
        for name, (selector, expected) in cases.items():
            with self.subTest(name):
                tokens = cli.tokenize_selector(selector)
                for kind in ("tag", "class", "attribute"):
                    self.assertEqual(tokens[kind], expected.get(kind, set()), kind)


class ExtractSelectorsTest(unittest.TestCase):
    """AST extraction rules for constants and locator calls."""

    def test_extracts_constants_and_calls(self) -> None:
        source = (
            'A_SEL, B_SEL_TMPL = ".a", \'.b[x="{xid}"]\'\n'
            'C_SEL = (\n    "xmc-c"\n    ".d"\n)\n'
            'NOT_A_SELECTOR = ".ignored"\n'
            "A_SEL, other = 1, 2\n"
            "def f(page, s):\n"
            '    page.locator(".e").first\n'
            '    page.wait_for_selector(f"{s}, .f")\n'
            "    page.locator(s)\n"
            '    page.goto(".ignored")\n'
        )
        texts = [s.text for s in _extract(source)]
        self.assertEqual(texts, [".a", '.b[x="{xid}"]', "xmc-c.d", ".e", "{}, .f"])

    def test_empty_source_has_no_selectors(self) -> None:
        self.assertEqual(_extract(""), [])

    def test_syntax_error_is_handled(self) -> None:
        with self.assertRaises(HandledError) as ctx:
            cli.parse_source("bad.py", "A_SEL = (\n")
        self.assertEqual(ctx.exception.details, {"file": "bad.py", "line": 1})


class FindCopyViolationsTest(unittest.TestCase):
    """Copy rule annotations on the same line or the line above."""

    def test_copy_rule(self) -> None:
        cases = {
            "unannotated_literal": ('A_SEL = ".t:has-text(\\"Go\\")"\n', 1),
            "same_line": ('A_SEL = ".t:has-text(\\"Go\\")"  # ui-contract: label\n', 0),
            "line_above": ('# ui-contract: label\nA_SEL = ".t:has-text(\\"Go\\")"\n', 0),
            "two_lines_above": ('# ui-contract: x\n\nA_SEL = ".t:has-text(\\"Go\\")"\n', 1),
            "empty_reason": ('# ui-contract:\nA_SEL = ".t:has-text(\\"Go\\")"\n', 1),
            "placeholder_text": ("A_SEL_TMPL = '.t:has-text(\"{tf}\")'\n", 1),
            "multibyte": ('A_SEL = ".t:has-text(\\"\u22125 Ünï\\")"\n', 1),
            "has_text_kwarg": ('page.locator(".r").filter(has_text="Done")\n', 1),
            "has_text_kwarg_annotated": (
                '# ui-contract: status\npage.locator(".r").filter(has_text="Done")\n',
                0,
            ),
            "has_text_variable": ('page.locator(".r").filter(has_text=name)\n', 0),
            "has_text_fstring": (
                'page.locator(".r").filter(has_text=f"Experiment #{xid}").first\n',
                1,
            ),
            "multiline_call": (
                "page.locator(\n    # ui-contract: tab label\n    f'.tab:has-text(\"{n}\")'\n)\n",
                0,
            ),
            "no_copy": ('A_SEL = ".t"\n', 0),
        }
        for name, (source, expected) in cases.items():
            with self.subTest(name):
                violations = _copy_violations(source)
                self.assertEqual(len(violations), expected, violations)
                self.assertTrue(all(v["rule"] == "copy" for v in violations))


class CheckUiSelectorContractTest(unittest.TestCase):
    """End-to-end checks against a temporary repository layout."""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        self._write("cmd/ui/app/page/p.ng.html", _BASE_TEMPLATE)
        self._write("cmd/ui/app/page/p.ts", _BASE_TS)
        self._write("cmd/ui/app/page/p_test.ts", "selector: 'xmc-test-only'\n")
        self._write("cmd/ui/index.html", "<xmc-root></xmc-root>\n")
        for pattern in cli.SELECTOR_SOURCES[1:]:
            self._write(pattern.replace("*", "x"), "")

    def _write(self, rel_path: str, content: str) -> None:
        path = self.root / rel_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def _run(self, css_source: str) -> dict:
        self._write(_CSS_SOURCE, css_source)
        return cli.check_ui_selector_contract(argparse.Namespace(repo_root=str(self.root)))

    def _violations(self, css_source: str) -> list[tuple[str, str]]:
        with self.assertRaises(HandledError) as ctx:
            self._run(css_source)
        details = ctx.exception.details or {}
        return [(v["rule"], v["token"]) for v in details["violations"]]

    def test_passes_known_dom(self) -> None:
        source = (
            'A_SEL, B_SEL = "xmc-root-page .queue-card", "xmc-child.is-open.a-two"\n'
            'C_SEL = "div.bare_key.tern-no.active-toggle, xmc-root span[data-row]"\n'
            'D_SEL = "input[placeholder][disabled][aria-label], a[href]"\n'
            'E_SEL = "mat-tab-header .mdc-tab .mat-row cdk-overlay-pane, body h1"\n'
            'F_SEL = "button.b-one[hidden]"\n'
        )
        self.assertEqual(self._run(source), {"selectors_scanned": 6, "violations": 0})

    def test_reports_missing_dom(self) -> None:
        cases = {
            "missing_class": ('A_SEL = "xmc-child .gone"\n', [("dom-class", "gone")]),
            "missing_tag": ('A_SEL = "xmc-gone .queue-card"\n', [("dom-tag", "xmc-gone")]),
            "test_only_selector": ('A_SEL = "xmc-test-only"\n', [("dom-tag", "xmc-test-only")]),
            "missing_attribute": ('A_SEL = "div[data-gone]"\n', [("dom-attribute", "data-gone")]),
            "interpolation_not_class": ('A_SEL = ".extra"\n', [("dom-class", "extra")]),
            "interpolation_tokens_not_classes": ('A_SEL = ".a"\n', [("dom-class", "a")]),
            "event_binding_not_attribute": ('A_SEL = "[click]"\n', [("dom-attribute", "click")]),
            "property_binding_not_attribute": (
                'A_SEL = "[value]"\n',
                [("dom-attribute", "value")],
            ),
            "copy": ('A_SEL = ".queue-card:has-text(\\"−5\\")"\n', [("copy", "has-text")]),
        }
        for name, (source, expected) in cases.items():
            with self.subTest(name):
                self.assertEqual(self._violations(source), expected)

    def test_empty_sources_pass(self) -> None:
        self.assertEqual(self._run(""), {"selectors_scanned": 0, "violations": 0})

    def test_missing_source_is_handled(self) -> None:
        (self.root / cli.SELECTOR_SOURCES[1]).unlink()
        with self.assertRaisesRegex(HandledError, "not found"):
            self._run("")

    def test_malformed_source_is_handled(self) -> None:
        with self.assertRaisesRegex(HandledError, "Cannot parse"):
            self._run("A_SEL = (\n")


if __name__ == "__main__":
    unittest.main()

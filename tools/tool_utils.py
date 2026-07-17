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

"""Utility module for formatting tool execution output and exceptions."""

import argparse
import json
import sys
import traceback


class HandledError(Exception):
    """Exception raised for errors that are handled with instructions."""

    def __init__(self, message, instruction):
        super().__init__(message)
        self.message = message
        self.instruction = instruction


def run_tool(parser: argparse.ArgumentParser, main_fn):
    """Standard runner for tools.

    - Handles `--help` automatically via argparse.
    - Captures exceptions and formats output as JSON.
    - Exit codes:
      0: Success
      1: Handled error
      2: Unhandled error
    """
    try:
        args = parser.parse_args()
        result = main_fn(args)

        output = {"status": "success", "result": result}
        print(json.dumps(output, indent=2))
        sys.exit(0)

    except HandledError as e:
        output = {
            "status": "error",
            "error_type": "handled",
            "message": e.message,
            "instruction": e.instruction,
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(1)

    except Exception as e:
        output = {
            "status": "error",
            "error_type": "unhandled",
            "message": str(e),
            "traceback": traceback.format_exc(),
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(2)

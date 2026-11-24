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

"""Single source of the xmc-metrics version.

pyproject.toml reads it as the distribution version, xmc re-exports it as
xmc.__version__, and the HTTP clients send it in their User-Agent headers. It lives
in its own module so those clients can import it without importing xmc/__init__.py.
"""

__version__ = "1.0.0.dev0"

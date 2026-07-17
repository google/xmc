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

def _extract_version(file):
    """Extracts the version number from a migration SQL file name.

    Expects filename format: <version_number>_<name>.[up|down].sql (e.g., "001_baseline.up.sql")
    """
    parts = file.basename.split("_")
    if not parts:
        return 0
    version_str = parts[0]
    if version_str.isdigit():
        return int(version_str)
    return 0

def _find_max_version(files):
    max_version = 0
    for f in files:
        version = _extract_version(f)
        if version > max_version:
            max_version = version
    return max_version

def _generate_schema_versions_json_impl(ctx):
    ess_max = _find_max_version(ctx.files.ess_srcs)
    xid_max = _find_max_version(ctx.files.xid_srcs)

    content = """{{
  "ess": {ess},
  "xid": {xid}
}}
""".format(
        ess = ess_max,
        xid = xid_max,
    )
    out = ctx.actions.declare_file(ctx.attr.out)
    ctx.actions.write(out, content)
    return [DefaultInfo(files = depset([out]))]

generate_schema_versions_json = rule(
    implementation = _generate_schema_versions_json_impl,
    attrs = {
        "ess_srcs": attr.label_list(allow_files = True),
        "xid_srcs": attr.label_list(allow_files = True),
        "out": attr.string(mandatory = True),
    },
)

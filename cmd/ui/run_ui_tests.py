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
"""Bazel test runner for Angular UI unit tests."""

import os
import pwd
import subprocess
import sys


def main():
  """Runs Angular UI unit tests in physical directory."""
  # Resolve the real physical directory of this script on the host system
  real_script_path = os.path.realpath(__file__)
  real_ui_dir = os.path.dirname(real_script_path)

  # Query passwd database for the actual host user's home directory
  try:
    user_home = pwd.getpwuid(os.getuid()).pw_dir
  except Exception:
    user_home = os.path.expanduser('~')

  # Check if NVM exists in the user's real home and source it
  nvm_sh = os.path.join(user_home, '.nvm', 'nvm.sh')
  if os.path.exists(nvm_sh):
    cmd = f'source {nvm_sh} && npx ng test --watch=false'
  else:
    cmd = 'npx ng test --watch=false'

  print(f'Executing Angular tests in physical workspace: {real_ui_dir}')

  # Run the command with bash so we can source nvm
  res = subprocess.run(
      cmd,
      shell=True,
      executable='/bin/bash',
      cwd=real_ui_dir,
      check=False,
  )
  sys.exit(res.returncode)


if __name__ == '__main__':
  main()

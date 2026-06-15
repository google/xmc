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
  # Resolve the real physical directory of this script on the host system
  real_script_path = os.path.realpath(__file__)
  real_ui_dir = os.path.dirname(real_script_path)

  # Query passwd database for the actual host user's home directory
  try:
    user_home = pwd.getpwuid(os.getuid()).pw_dir
  except Exception:
    user_home = os.path.expanduser('~')

  # Ensure ~/.local/bin and standard node paths are in PATH
  local_bin = os.path.join(user_home, '.local', 'bin')
  os.environ['PATH'] = f"{local_bin}:{os.environ.get('PATH', '')}:/usr/local/bin"

  print(f'Syncing Angular UI workspace from {real_ui_dir} to dedicated hermetic cache...')
  cache_dir = os.path.join(user_home, '.cache', 'xmc_ui_test_env')
  os.makedirs(cache_dir, exist_ok=True)

  # Use rsync to mirror the UI workspace cleanly without the broken node_modules
  rsync_cmd = f"rsync -a --delete --exclude=node_modules/ {real_ui_dir}/ {cache_dir}/"
  subprocess.run(rsync_cmd, shell=True, check=True)

  # Check if npm install is needed in the clean cache directory
  if not os.path.exists(os.path.join(cache_dir, 'node_modules', '.bin', 'eslint')):
    print('Installing clean npm dependencies in hermetic environment...')
    npm_cmd = f'export PATH="{os.environ["PATH"]}" && npm install --force --legacy-peer-deps'
    subprocess.run(npm_cmd, shell=True, executable='/bin/bash', cwd=cache_dir, check=True)

  # Check if NVM exists in the user's real home and source it
  nvm_sh = os.path.join(user_home, '.nvm', 'nvm.sh')
  sync_back = f'rsync -a {cache_dir}/app/ {real_ui_dir}/app/'
  lint_and_test = f'npm run lint -- --fix; {sync_back}; npm run lint && npx ng test --watch=false'
  if os.path.exists(nvm_sh):
    cmd = f'source {nvm_sh} && export PATH="{os.environ["PATH"]}" && {lint_and_test}'
  else:
    cmd = f'export PATH="{os.environ["PATH"]}" && {lint_and_test}'

  print(f'Executing Angular tests hermetically in: {cache_dir}')

  # Run the command with bash so we can source nvm
  res = subprocess.run(
      cmd,
      shell=True,
      executable='/bin/bash',
      cwd=cache_dir,
      check=False,
  )
  sys.exit(res.returncode)



if __name__ == '__main__':
  main()

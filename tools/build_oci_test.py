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

import unittest
from unittest.mock import patch, mock_open, MagicMock
import os
import tarfile

# Import using root-relative path because we added imports = ["."] in Bazel
from build_oci import build_oci, EXEC_PERMISSION, FILE_PERMISSION
from tool_utils import HandledError

class TestBuildOci(unittest.TestCase):

    @patch('os.path.exists')
    def test_build_oci_binary_not_found(self, mock_exists):
        mock_exists.return_value = False
        args = MagicMock()
        args.binary_path = "/path/to/missing_binary"
        args.binary_name = "test-service"
        args.image_tag = "test-service:latest"
        args.output_tar = "out.tar"

        with self.assertRaises(HandledError) as ctx:
            build_oci(args)
        
        self.assertIn("Binary path not found", ctx.exception.message)
        self.assertIn("Ensure that the binary is built", ctx.exception.instruction)

    @patch('os.path.exists')
    @patch('os.path.realpath')
    @patch('tarfile.open')
    @patch('builtins.open', new_callable=mock_open)
    @patch('hashlib.sha256')
    @patch('os.rename')
    @patch('os.remove')
    def test_build_oci_success(self, mock_remove, mock_rename, mock_sha, mock_builtin_open, mock_tarfile_open, mock_realpath, mock_exists):
        # Mock paths exists checks.
        # We need layer.tar, final config file, and manifest.json to "exist" so cleanup os.remove is called.
        def exists_side_effect(path):
            if path in ("/path/to/binary", "/etc/ssl/certs/ca-certificates.crt"):
                return True
            if path in ("layer.tar", "config.json", "manifest.json", "configsha.json"):
                return True
            return False
            
        mock_exists.side_effect = exists_side_effect
        mock_realpath.return_value = "/path/to/binary"
        
        # Mock sha256.hexdigest for layer and config
        mock_sha_obj = MagicMock()
        mock_sha_obj.hexdigest.side_effect = ["layersha", "configsha"]
        mock_sha.return_value = mock_sha_obj

        # Mock tarfile.open
        mock_tar = MagicMock()
        mock_tarfile_open.return_value.__enter__.return_value = mock_tar
        
        # Mock tar.gettarinfo to return mock info objects
        mock_info_bin = MagicMock()
        mock_info_bin.mode = 0
        mock_info_ca = MagicMock()
        mock_info_ca.mode = 0
        
        def gettarinfo_side_effect(name, arcname=None):
            if name == "/path/to/binary":
                return mock_info_bin
            if name == "/etc/ssl/certs/ca-certificates.crt":
                return mock_info_ca
            return MagicMock()

        mock_tar.gettarinfo.side_effect = gettarinfo_side_effect

        args = MagicMock()
        args.binary_path = "/path/to/binary"
        args.binary_name = "test-service"
        args.image_tag = "test-service:latest"
        args.output_tar = "out.tar"

        res = build_oci(args)

        # Assert results
        self.assertEqual(res["output_tar"], "out.tar")
        self.assertEqual(res["image_tag"], "test-service:latest")
        self.assertEqual(res["layer_sha256"], "layersha")
        self.assertEqual(res["config_sha256"], "configsha")

        # Verify permissions were set correctly
        self.assertEqual(mock_info_bin.mode, EXEC_PERMISSION)
        self.assertEqual(mock_info_ca.mode, FILE_PERMISSION)

        # Check clean-up is executed
        mock_remove.assert_any_call("layer.tar")
        mock_remove.assert_any_call("configsha.json")
        mock_remove.assert_any_call("manifest.json")

    @patch('os.path.exists')
    @patch('os.path.realpath')
    @patch('tarfile.open')
    @patch('builtins.open', new_callable=mock_open)
    @patch('os.remove')
    def test_build_oci_tarfile_error(self, mock_remove, mock_builtin_open, mock_tarfile_open, mock_realpath, mock_exists):
        mock_exists.side_effect = lambda path: path == "/path/to/binary" or path == "layer.tar"
        mock_realpath.return_value = "/path/to/binary"
        
        # Make tarfile.open raise an exception
        mock_tarfile_open.side_effect = Exception("tar error")

        args = MagicMock()
        args.binary_path = "/path/to/binary"
        args.binary_name = "test-service"
        args.image_tag = "test-service:latest"
        args.output_tar = "out.tar"

        with self.assertRaises(HandledError) as ctx:
            build_oci(args)
        
        self.assertIn("Failed to create layer tarball: tar error", ctx.exception.message)
        mock_remove.assert_any_call("layer.tar")

if __name__ == '__main__':
    unittest.main()

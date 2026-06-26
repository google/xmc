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
import json
import tempfile

from build_oci import build_oci, detect_elf_architecture, EXEC_PERMISSION, FILE_PERMISSION
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

    @patch('builtins.open', new_callable=mock_open)
    def test_detect_elf_architecture_amd64(self, mock_builtin_open):
        elf_header_amd64 = bytearray(64)
        elf_header_amd64[0:4] = b"\x7fELF"
        elf_header_amd64[5] = 1 # Little endian
        elf_header_amd64[18] = 62 # EM_X86_64
        
        mock_builtin_open.return_value.read.return_value = bytes(elf_header_amd64)
        
        arch = detect_elf_architecture("/path/to/binary")
        self.assertEqual(arch, "amd64")

    @patch('builtins.open', new_callable=mock_open)
    def test_detect_elf_architecture_arm64(self, mock_builtin_open):
        elf_header_arm64 = bytearray(64)
        elf_header_arm64[0:4] = b"\x7fELF"
        elf_header_arm64[5] = 1 # Little endian
        elf_header_arm64[18] = 183 # EM_AARCH64
        
        mock_builtin_open.return_value.read.return_value = bytes(elf_header_arm64)
        
        arch = detect_elf_architecture("/path/to/binary")
        self.assertEqual(arch, "arm64")

    @patch('builtins.open', new_callable=mock_open)
    def test_detect_elf_architecture_non_elf(self, mock_builtin_open):
        mock_builtin_open.return_value.read.return_value = b"NOT_AN_ELF_FILE_FOR_SURE"
        
        arch = detect_elf_architecture("/path/to/binary")
        self.assertIsNone(arch)

    @patch('os.path.exists')
    @patch('os.path.realpath')
    @patch('tarfile.open')
    @patch('builtins.open', new_callable=mock_open)
    @patch('hashlib.sha256')
    @patch('os.rename')
    @patch('tempfile.TemporaryDirectory')
    def test_build_oci_success_amd64(self, mock_tempdir_class, mock_rename, mock_sha, mock_builtin_open, mock_tarfile_open, mock_realpath, mock_exists):
        # Mock TemporaryDirectory context manager
        mock_tempdir = MagicMock()
        mock_tempdir.__enter__.return_value = "/tmp/mock-dir"
        mock_tempdir_class.return_value = mock_tempdir

        # 1. Prepare ELF header for AMD64
        elf_header = bytearray(64)
        elf_header[0:4] = b"\x7fELF"
        elf_header[5] = 1 # Little endian
        elf_header[18] = 62 # EM_X86_64
        
        # 2. Mock paths exists checks
        def exists_side_effect(path):
            if path in ("/path/to/binary", "/etc/ssl/certs/ca-certificates.crt"):
                return True
            return False
            
        mock_exists.side_effect = exists_side_effect
        mock_realpath.return_value = "/path/to/binary"
        
        # 3. Custom open mock to handle multiple path reads
        written_config = []
        def custom_open(path, mode="r", *args, **kwargs):
            if path == "/path/to/binary":
                return mock_open(read_data=bytes(elf_header))()
            elif "config.json" in path and "w" in mode:
                # Catch the config content
                f = MagicMock()
                f.__enter__.return_value = f
                def write_side_effect(data):
                    written_config.append(data)
                f.write.side_effect = write_side_effect
                return f
            return mock_open(read_data=b"mock_data")()
            
        mock_builtin_open.side_effect = custom_open
        
        # Mock sha256
        mock_sha_obj = MagicMock()
        mock_sha_obj.hexdigest.side_effect = ["layersha", "configsha"]
        mock_sha.return_value = mock_sha_obj

        # Mock tarfile.open
        mock_tar = MagicMock()
        mock_tarfile_open.return_value.__enter__.return_value = mock_tar
        
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

        self.assertEqual(res["output_tar"], "out.tar")
        self.assertEqual(res["image_tag"], "test-service:latest")
        self.assertEqual(res["layer_sha256"], "layersha")
        self.assertEqual(res["config_sha256"], "configsha")

        # Verify parsed config architecture is amd64
        parsed_config = json.loads("".join(written_config))
        self.assertEqual(parsed_config["architecture"], "amd64")

        # Verify rename was called inside the mock directory
        mock_rename.assert_called_once_with("/tmp/mock-dir/config.json", "/tmp/mock-dir/configsha.json")

    @patch('os.path.exists')
    @patch('os.path.realpath')
    @patch('tarfile.open')
    @patch('builtins.open', new_callable=mock_open)
    @patch('hashlib.sha256')
    @patch('os.rename')
    @patch('tempfile.TemporaryDirectory')
    def test_build_oci_success_arm64(self, mock_tempdir_class, mock_rename, mock_sha, mock_builtin_open, mock_tarfile_open, mock_realpath, mock_exists):
        # Mock TemporaryDirectory context manager
        mock_tempdir = MagicMock()
        mock_tempdir.__enter__.return_value = "/tmp/mock-dir"
        mock_tempdir_class.return_value = mock_tempdir

        # 1. Prepare ELF header for ARM64
        elf_header = bytearray(64)
        elf_header[0:4] = b"\x7fELF"
        elf_header[5] = 1 # Little endian
        elf_header[18] = 183 # EM_AARCH64
        
        # 2. Mock paths exists checks
        def exists_side_effect(path):
            if path in ("/path/to/binary", "/etc/ssl/certs/ca-certificates.crt"):
                return True
            return False
            
        mock_exists.side_effect = exists_side_effect
        mock_realpath.return_value = "/path/to/binary"
        
        # 3. Custom open mock to handle multiple path reads
        written_config = []
        def custom_open(path, mode="r", *args, **kwargs):
            if path == "/path/to/binary":
                return mock_open(read_data=bytes(elf_header))()
            elif "config.json" in path and "w" in mode:
                f = MagicMock()
                f.__enter__.return_value = f
                def write_side_effect(data):
                    written_config.append(data)
                f.write.side_effect = write_side_effect
                return f
            return mock_open(read_data=b"mock_data")()
            
        mock_builtin_open.side_effect = custom_open
        
        mock_sha_obj = MagicMock()
        mock_sha_obj.hexdigest.side_effect = ["layersha", "configsha"]
        mock_sha.return_value = mock_sha_obj

        mock_tar = MagicMock()
        mock_tarfile_open.return_value.__enter__.return_value = mock_tar
        
        mock_info_bin = MagicMock()
        mock_info_bin.mode = 0
        mock_info_ca = MagicMock()
        mock_info_ca.mode = 0
        
        mock_tar.gettarinfo.side_effect = lambda name, arcname=None: mock_info_bin if name == "/path/to/binary" else mock_info_ca

        args = MagicMock()
        args.binary_path = "/path/to/binary"
        args.binary_name = "test-service"
        args.image_tag = "test-service:latest"
        args.output_tar = "out.tar"

        res = build_oci(args)

        parsed_config = json.loads("".join(written_config))
        self.assertEqual(parsed_config["architecture"], "arm64")

    @patch('os.path.exists')
    @patch('os.path.realpath')
    @patch('builtins.open', new_callable=mock_open)
    def test_build_oci_failure_non_elf(self, mock_builtin_open, mock_realpath, mock_exists):
        mock_exists.return_value = True
        mock_realpath.return_value = "/path/to/binary"
        
        # Make the open return non-ELF content
        mock_builtin_open.return_value.read.return_value = b"NOT_AN_ELF_FILE"

        args = MagicMock()
        args.binary_path = "/path/to/binary"
        args.binary_name = "test-service"
        args.image_tag = "test-service:latest"
        args.output_tar = "out.tar"

        with self.assertRaises(HandledError) as ctx:
            build_oci(args)

        self.assertIn("is not a valid Linux ELF file", ctx.exception.message)
        self.assertIn("ensure the binary is cross-compiled", ctx.exception.instruction)

if __name__ == '__main__':
    unittest.main()

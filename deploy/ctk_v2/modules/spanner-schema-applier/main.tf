/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

resource "null_resource" "apply_schema" {
  triggers = {
    schema_hash   = filesha256(var.schema_file)
    instance_name = var.instance_name
    database_name = var.database_name
    project_id    = var.project_id
  }

  provisioner "local-exec" {
    command = <<EOF
      set -e
      echo "Locating workspace root..."
      curr_dir="$PWD"
      workspace_root=""
      while [[ "$curr_dir" != "/" ]]; do
        if [[ -f "$curr_dir/WORKSPACE" ]]; then
          workspace_root="$curr_dir"
          break
        fi
        curr_dir="$(dirname "$curr_dir")"
      done

      if [[ -z "$workspace_root" ]]; then
        echo "Error: Could not find WORKSPACE file in parent directories" >&2
        exit 1
      fi

      if [[ "${var.db_type}" == "ess" ]]; then
        proto_target="//api/protos/experiment:experiment_messages_descriptor"
        proto_output_rel="bazel-bin/api/protos/experiment/experiment_messages_descriptor.pb"
      elif [[ "${var.db_type}" == "xid" ]]; then
        proto_target="//api/protos/xid:xid_messages_descriptor"
        proto_output_rel="bazel-bin/api/protos/xid/xid_messages_descriptor.pb"
      else
        echo "Unknown db_type: ${var.db_type}" >&2
        exit 1
      fi

      echo "Building proto descriptors using target $proto_target..."
      (cd "$workspace_root" && bazel build "$proto_target")
      proto_file="$workspace_root/$proto_output_rel"

      echo "Applying schema ${var.schema_file} to ${var.database_name}..."
      gcloud spanner databases ddl update "${var.database_name}" \
        --instance="${var.instance_name}" \
        --project="${var.project_id}" \
        --ddl-file="${var.schema_file}" \
        --proto-descriptors-file="$proto_file"

      echo "Initializing migration version for ${var.db_type} database..."
      (cd "$workspace_root" && bazel run //internal/storage/spanner/migrations:migrations -- \
        --project="${var.project_id}" \
        --instance="${var.instance_name}" \
        --database="${var.database_name}" \
        --migrations_dir="${var.db_type}" \
        --command="init")
    EOF
  }
}

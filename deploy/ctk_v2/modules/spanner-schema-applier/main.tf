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
    proto_hash    = var.proto_descriptors_file != null ? filesha256(var.proto_descriptors_file) : ""
    instance_name = var.instance_name
    database_name = var.database_name
    project_id    = var.project_id
  }

  provisioner "local-exec" {
    command = <<EOF
      set -e
      echo "Applying schema ${var.schema_file} to ${var.database_name}..."
      gcloud spanner databases ddl update "${var.database_name}" \
        --instance="${var.instance_name}" \
        --project="${var.project_id}" \
        --ddl-file="${var.schema_file}" \
        ${var.proto_descriptors_file != null ? "--proto-descriptors-file=\"${var.proto_descriptors_file}\"" : ""}

      echo "Initializing migration version for ${var.db_type} database..."
      export GOOGLE_CLOUD_PROJECT="${var.project_id}"
      run_spanner_migration "${var.instance_name}" "${var.database_name}" "${var.db_type}" "init"
    EOF
  }
}

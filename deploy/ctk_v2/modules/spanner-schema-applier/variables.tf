variable "project_id" {
  type        = string
  description = "GCP Project ID"
}

variable "instance_name" {
  type        = string
  description = "Spanner Instance Name"
}

variable "database_name" {
  type        = string
  description = "Spanner Database Name"
}

variable "schema_file" {
  type        = string
  description = "Path to the schema SQL file"
}



variable "db_type" {
  type        = string
  description = "Database type (e.g. ess or xid)"
}


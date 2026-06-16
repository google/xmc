output "certificate_map_name" {
  value       = google_certificate_manager_certificate_map.default.name
  description = "The name of the certificate map"
}

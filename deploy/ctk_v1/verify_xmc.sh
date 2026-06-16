#!/bin/bash
# Updated Verification Script for XManager on Cloud (XMC)

PROJECT_ID=$(gcloud config get-value project)
REGION="us-central1"

echo "========================================="
echo "XManager on Cloud (XMC) Verification"
echo "========================================="

echo "\n--- 1. Pod Status ---"
kubectl get pods -n xmc

echo "\n--- 2. Service Status ---"
kubectl get svc -n xmc

echo "\n--- 3. Ingress Status & IPs ---"
kubectl get ingress -n xmc

echo "\n--- 4. Managed Certificates ---"
kubectl get managedcertificate -n xmc
echo "Note: If status is ProvisioningFailed, external HTTPS might not work."

echo "\n--- 5. Cloud Run UI URL ---"
UI_URL=$(gcloud run services describe xmc-ui-dev --region $REGION --project $PROJECT_ID --format='value(status.url)')
echo "UI URL: $UI_URL"

echo "\n--- 6. Test Commands ---"

echo "To test the UI (returns HTML if available):"
echo "TOKEN=\$(gcloud auth print-identity-token)"
echo "curl -H \"Authorization: Bearer \$TOKEN\" $UI_URL"

echo "\nTo test Backend (HTTP) via Load Balancer (from workstation with host file mapping):"
echo "If you have mapped 'api.dev.xmc.example.com' to the Ingress IP in your /etc/hosts:"
echo "TOKEN=\$(gcloud auth print-identity-token)"
echo "curl -H \"Authorization: Bearer \$TOKEN\" https://api.dev.xmc.example.com"

echo "\nTo test Backend (HTTP) directly via IP (if IAP allows/bypasses SSL cert failure):"
HTTP_IP=$(kubectl get ingress http-ingress -n xmc -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "HTTP_IP: $HTTP_IP"
echo "TOKEN=\$(gcloud auth print-identity-token)"
echo "curl -H \"Host: api.dev.xmc.example.com\" -H \"Authorization: Bearer \$TOKEN\" http://$HTTP_IP"

echo "\nTo test internally (Port-Forwarding Actuator):"
ACTUATOR_POD=$(kubectl get pods -n xmc -l app=actuator -o jsonpath='{.items[0].metadata.name}')
echo "kubectl port-forward -n xmc \$ACTUATOR_POD 8080:8080"
echo "In another terminal run: curl localhost:8080"
echo "Note: 404 is expected for root path. Try specific paths if known."

echo "\nTo test internally (Port-Forwarding XID-service):"
XID_POD=$(kubectl get pods -n xmc -l app=xid-service -o jsonpath='{.items[0].metadata.name}')
echo "kubectl port-forward -n xmc \$XID_POD 50051:50051"
echo "In another terminal run: curl localhost:50051 (Requires gRPC client, HTTP might return error)."

echo "========================================="

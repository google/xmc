#!/bin/bash
# Updated Verification Script for XManager on Cloud v2 Topology

echo "========================================="
echo "XManager on Cloud (XMC) v2 Verification"
echo "========================================="

echo -e "\n--- 1. Pod Status ---"
kubectl get pods -n xmc

echo -e "\n--- 2. Service Status ---"
kubectl get svc -n xmc

echo -e "\n--- 3. GKE Gateway Status ---"
kubectl get gateway -n xmc

echo -e "\n--- 4. HTTPRoutes ---"
kubectl get httproute -n xmc

echo -e "\n--- 5. cert-manager Certificates ---"
kubectl get certificate -n xmc

echo -e "\n--- 6. Test Connectivity ---"
GATEWAY_IP=$(kubectl get gateway xmc-gateway -n xmc -o jsonpath='{.status.addresses[0].value}')
echo "Gateway IP Address: $GATEWAY_IP"
echo "Make sure your domains resolve to $GATEWAY_IP."

echo -e "\nTo test local endpoint routing internally (Port-Forwarding Actuator):"
ACTUATOR_POD=$(kubectl get pods -n xmc -l app=actuator -o jsonpath='{.items[0].metadata.name}')
echo "Run: kubectl port-forward -n xmc $ACTUATOR_POD 8080:8080"
echo "In another terminal run: curl http://localhost:8080/actuate/health"

echo "========================================="

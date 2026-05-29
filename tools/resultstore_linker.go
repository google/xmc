//go:build ignore
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/devtools/resultstore/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/google"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func main() {
	action := flag.String("action", "", "Action to perform: patch or finalize")
	invocationID := flag.String("invocation_id", "", "The UUID of the ResultStore invocation")
	projectID := flag.String("project_id", "", "The GCP Project ID")
	bucket := flag.String("bucket", "", "The GCS bucket where logs are stored")
	statusStr := flag.String("status", "", "Final status: passed or failed")
	flag.Parse()

	if *invocationID == "" || *projectID == "" {
		log.Fatal("invocation_id and project_id are required")
	}

	ctx := context.Background()

	// Use native Application Default Credentials.
	// This matches Bazel's identity exchange logic exactly.
	conn, err := grpc.DialContext(ctx, "resultstore.googleapis.com:443",
		grpc.WithCredentialsBundle(google.NewDefaultCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect to ResultStore gRPC: %v", err)
	}
	defer conn.Close()

	uploadClient := resultstore.NewResultStoreUploadClient(conn)

	// Add Project ID to metadata for correct quota/billing attribution.
	ctx = metadata.AppendToOutgoingContext(ctx, "x-goog-user-project", *projectID)

	// Canonical Resource Name
	name := fmt.Sprintf("invocations/%s", *invocationID)

	switch *action {
	case "patch":
		if *bucket == "" {
			log.Fatal("bucket is required for patch action")
		}

		gcsURI := fmt.Sprintf("googlefile:/bigstore/%s/%s/test.log", *bucket, *invocationID)
		
		invocation := &resultstore.Invocation{
			Name: name,
			Files: []*resultstore.File{
				{
					Uid:         "test.log",
					Uri:         gcsURI,
					ContentType: "text/plain",
				},
			},
		}

		updateMaskPaths := []string{"files"}
		if *statusStr != "" {
			rsStatus := resultstore.Status_PASSED
			if strings.ToLower(*statusStr) == "failed" {
				rsStatus = resultstore.Status_FAILED
			}
			invocation.StatusAttributes = &resultstore.StatusAttributes{Status: rsStatus}
			updateMaskPaths = append(updateMaskPaths, "status_attributes")
		}

		req := &resultstore.UpdateInvocationRequest{
			Invocation: invocation,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: updateMaskPaths},
			// NO AuthorizationToken here - we rely on direct identity ownership.
		}

		fmt.Printf("[*] Attempting to patch invocation %s with log: %s\n", name, gcsURI)
		
		var finalErr error
		for i := 0; i < 20; i++ {
			_, err = uploadClient.UpdateInvocation(ctx, req)
			if err == nil {
				fmt.Println("[+] SUCCESS: Record patched via gRPC.")
				return
			}
			
			finalErr = err
			st, ok := status.FromError(err)
			if ok && (st.Code() == codes.NotFound || st.Code() == codes.Unavailable) {
				fmt.Printf("[-] Attempt %d: Record not visible yet. Retrying in 10s...\n", i+1)
				time.Sleep(10 * time.Second)
				continue
			}
			
			log.Fatalf("[!] Patch failed with permanent error: %v", err)
		}
		log.Fatalf("[!] ERROR: Linker timed out waiting for record: %v", finalErr)

	case "finalize":
		fmt.Printf("[*] Finalizing record %s via gRPC...\n", name)
		req := &resultstore.FinalizeInvocationRequest{
			Name: name,
			// NO AuthorizationToken here.
		}
		_, err := uploadClient.FinalizeInvocation(ctx, req)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already finalized") {
				fmt.Println("[!] INFO: Record already finalized.")
				return
			}
			log.Fatalf("[!] Finalize failed: %v", err)
		}
		fmt.Println("[+] SUCCESS: Record finalized.")

	default:
		log.Fatalf("Unknown action: %s", *action)
	}
}

package main

import "testing"

func TestRequireLocalEndpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://127.0.0.1:8000",
		"http://localhost:8000",
		"http://[::1]:8000",
		"http://dynamodb:8000",
	} {
		endpoint := endpoint
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			if err := requireLocalEndpoint(endpoint); err != nil {
				t.Fatalf("requireLocalEndpoint(%q): %v", endpoint, err)
			}
		})
	}
}

func TestRequireLocalEndpointRejectsAWSAndEmptyEndpoints(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"", "https://dynamodb.us-east-2.amazonaws.com"} {
		if err := requireLocalEndpoint(endpoint); err == nil {
			t.Fatalf("requireLocalEndpoint(%q) succeeded", endpoint)
		}
	}
}

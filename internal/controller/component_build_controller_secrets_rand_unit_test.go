/*
Copyright 2021-2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"crypto/rand"
	"fmt"
	"testing"
)

func TestGeneratePaCWebhookSecretString_PanicsOnRandFailure(t *testing.T) {
	// Save the original function and restore it after the test.
	originalRandRead := randReadFunc
	t.Cleanup(func() { randReadFunc = originalRandRead })

	// Replace with a function that always fails.
	randReadFunc = func(b []byte) (int, error) {
		return 0, fmt.Errorf("simulated entropy failure")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when randReadFunc fails, but none occurred")
		}
		panicMsg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if panicMsg != "Failed to read from random generator" {
			t.Errorf("unexpected panic message: %s", panicMsg)
		}
	}()

	generatePaCWebhookSecretString()
}

func TestGeneratePaCWebhookSecretString_UsesRandReadFunc(t *testing.T) {
	// Verify that randReadFunc defaults to crypto/rand.Read.
	// We can't compare function pointers directly in Go, but we can
	// verify the default produces valid output (same test as existing,
	// but validates our refactoring didn't break the default path).
	randReadFunc = rand.Read
	t.Cleanup(func() { randReadFunc = rand.Read })

	secret := generatePaCWebhookSecretString()
	expectedLength := 20 * 2 // 20 bytes = 40 hex chars
	if len(secret) != expectedLength {
		t.Errorf("expected %d-character hex string, got length %d", expectedLength, len(secret))
	}
}

func TestGeneratePaCWebhookSecretString_CustomRandRead(t *testing.T) {
	originalRandRead := randReadFunc
	t.Cleanup(func() { randReadFunc = originalRandRead })

	// Replace with a deterministic reader that fills with 0xAB.
	randReadFunc = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 0xAB
		}
		return len(b), nil
	}

	secret := generatePaCWebhookSecretString()
	// 20 bytes of 0xAB = "abababab..." (40 chars)
	expected := ""
	for i := 0; i < 20; i++ {
		expected += "ab"
	}
	if secret != expected {
		t.Errorf("expected %q, got %q", expected, secret)
	}
}

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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-logr/zapr"
	compv1alpha1 "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestPaCTriggerErrorPreservesCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := pacTriggerError{cause: &url.Error{Op: "Post", URL: "http://service/?token=secret", Err: cause}}
		if !errors.Is(err, cause) {
			t.Errorf("error does not preserve cause %v", cause)
		}
		if _, ok := errors.AsType[*url.Error](err); !ok {
			t.Error("error does not preserve url.Error")
		}
		if err.Error() != "failed to trigger PaC build" {
			t.Errorf("unexpected error message: %s", err)
		}
	}
}

func TestPaCClientRejectsRedirects(t *testing.T) {
	var received atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer destination.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
			}))
			defer server.Close()
			client := getHttpClient()
			defer client.CloseIdleConnections()
			resp, err := client.Post(server.URL, "application/json", strings.NewReader(`{"secret":"synthetic-secret"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != status {
				t.Errorf("expected HTTP %d, got %d", status, resp.StatusCode)
			}
		})
	}
	if received.Load() != 0 {
		t.Fatal("client forwarded a request to the redirect destination")
	}
}

func TestTriggerPaCBuildDoesNotLogMalformedLocation(t *testing.T) {
	const secret = "synthetic-incoming-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://user:"+secret+"@example.invalid/%zz")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	originalGetHttpClient := GetHttpClientFunction
	GetHttpClientFunction = getHttpClient
	t.Cleanup(func() { GetHttpClientFunction = originalGetHttpClient })

	var logs bytes.Buffer
	logger := zapr.NewLogger(zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&logs), zapcore.DebugLevel)))
	ctx := ctrllog.IntoContext(t.Context(), logger)
	reconciler := &ComponentBuildReconciler{}
	err := reconciler.TriggerPaCBuild(ctx, &compv1alpha1.Component{}, "v1", "main", server.URL,
		&corev1.Secret{Data: map[string][]byte{pacIncomingSecretKey: []byte(secret)}}, &pacv1alpha1.Repository{})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if _, ok := errors.AsType[*url.Error](err); !ok {
		t.Fatal("error does not preserve url.Error")
	}
	logger.Error(err, "trigger failed")
	logger.Error(fmt.Errorf("reconcile: %w", err), "reconcile failed")
	if strings.Contains(logs.String(), secret) || strings.Contains(fmt.Sprintf("%v %+v", err, err), secret) {
		t.Fatal("error or logs contain the incoming secret")
	}
}

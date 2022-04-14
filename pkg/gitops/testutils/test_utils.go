/*
Copyright 2021 Red Hat, Inc.

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
package testutils

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Mocking of K8s client that allows unit testing functions that load cluster resources
// The function variables are meant to be overriden during the tests runtime
type MockedClient struct {
	MockedGet func(obj client.Object) error
}

func (c MockedClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return nil
}

func (c MockedClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (c MockedClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return nil
}

func (c MockedClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object) error {
	return c.MockedGet(obj)
}

func (c MockedClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func (c MockedClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (c MockedClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (c MockedClient) Scheme() *runtime.Scheme {
	return nil
}

func (c MockedClient) Status() client.StatusWriter {
	return nil
}

func (c MockedClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return nil
}

---
name: Run integration test (envtest)
description: Use this skill to run a specific integration test
---

To run a specific integration test execute:
```sh
KUBEBUILDER_ASSETS=$(echo "$(pwd)"/bin/k8s/*-linux-amd64) go test -count=1 -v ./internal/controller -ginkgo.focus="test or suite name"
```

To run all integration tests:
```sh
make test
```
Note, they take about 5 minutes to complete.

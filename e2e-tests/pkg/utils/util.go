package utils

import (
	"context"
	"os"
	"time"

	"github.com/devfile/library/v2/pkg/util"
	"k8s.io/apimachinery/pkg/util/wait"
)

func GetGeneratedNamespace(name string) string {
	return name + "-" + util.GenerateRandomString(4)
}

// Retrieve an environment variable. If it does not exists, default value will be returned
func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func WaitUntilWithInterval(cond wait.ConditionFunc, interval time.Duration, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(context.Background(), interval, timeout, true, func(ctx context.Context) (bool, error) { return cond() })
}

func WaitUntil(cond wait.ConditionFunc, timeout time.Duration) error {
	return WaitUntilWithInterval(cond, time.Second, timeout)
}

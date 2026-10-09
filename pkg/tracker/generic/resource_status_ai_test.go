//go:build ai_tests

package generic

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func objectFromYAML(t *testing.T, manifest string) *unstructured.Unstructured {
	t.Helper()

	var content map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &content))

	return &unstructured.Unstructured{Object: content}
}

const cnpgClusterHeader = "apiVersion: postgresql.cnpg.io/v1\nkind: Cluster\nmetadata:\n  name: test-pg\n"

func TestAI_CNPGClusterReadiness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		ready    bool
	}{
		{"no status at all", cnpgClusterHeader, false},
		{"Ready=False", cnpgClusterHeader + "status:\n  phase: Setting up primary\n  conditions:\n  - type: Ready\n    status: \"False\"\n", false},
		{"Ready=Unknown", cnpgClusterHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"Unknown\"\n", false},
		{"only unrelated conditions", cnpgClusterHeader + "status:\n  conditions:\n  - type: ContinuousArchiving\n    status: \"True\"\n", false},
		{"Ready=True", cnpgClusterHeader + "status:\n  phase: Cluster in healthy state\n  conditions:\n  - type: Ready\n    status: \"True\"\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := NewResourceStatus(objectFromYAML(t, tc.manifest))
			require.NoError(t, err)

			assert.Equal(t, tc.ready, status.IsReady())
			assert.False(t, status.IsFailed(), "Ready=False is the normal bootstrap state, never a failure")
			assert.Equal(t, "status.conditions[type=Ready].status", status.HumanConditionPath())
		})
	}
}

func TestAI_ImplicitReadyFallbackForUnknownResource(t *testing.T) {
	status, err := NewResourceStatus(objectFromYAML(t, "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: w\n"))
	require.NoError(t, err)

	assert.True(t, status.IsReady(), "a resource with no recognized status field is considered ready immediately")
	assert.Empty(t, status.HumanConditionPath())
	assert.Nil(t, status.Indicator)
}

// The fallback diagnostic must stay debug-only: it is the normal path for the many
// custom resources that legitimately have no status, so printing it by default would
// be noise on every deploy.
func TestAI_ImplicitReadyFallbackLogsOnlyInDebugMode(t *testing.T) {
	statusless := "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: my-widget\n"
	tracked := "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: tracked-widget\nstatus:\n  phase: Running\n"

	for _, tc := range []struct {
		name       string
		debug      string
		manifest   string
		wantOutput bool
	}{
		{"debug off, no status field", "", statusless, false},
		{"debug on, no status field", "1", statusless, true},
		{"debug on, status field matched", "1", tracked, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBEDOG_TRACKER_DEBUG", tc.debug)

			output := captureStdout(t, func() {
				_, err := NewResourceStatus(objectFromYAML(t, tc.manifest))
				require.NoError(t, err)
			})

			if !tc.wantOutput {
				assert.Empty(t, output)
				return
			}

			assert.Equal(t, "`my-widget` no recognized status field found, considering ready immediately\n", output)
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { reader.Close() })

	original := os.Stdout
	os.Stdout = writer

	// Restore stdout and close the writer even if fn panics, so a failing test cannot leave
	// the rest of the binary writing into a dead pipe.
	func() {
		defer func() {
			os.Stdout = original
			writer.Close()
		}()

		fn()
	}()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	require.NoError(t, err)

	return buf.String()
}

func TestAI_ResourceStatusIsConcurrencySafe(t *testing.T) {
	const iterations = 4000

	pending := objectFromYAML(t, "apiVersion: example.io/v1\nkind: Widget\nstatus:\n  phase: Pending\n")
	running := objectFromYAML(t, "apiVersion: example.io/v1\nkind: Widget\nstatus:\n  phase: Running\n")

	var wg sync.WaitGroup
	pendingErrs := make(chan string, iterations*2)

	for i := 0; i < iterations; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			status, err := NewResourceStatus(pending)
			if err != nil {
				pendingErrs <- fmt.Sprintf("pending: %s", err)
				return
			}
			if status.IsReady() {
				pendingErrs <- "pending resource reported ready"
			}
			if status.Indicator.Value != "Pending" {
				pendingErrs <- fmt.Sprintf("pending resource indicator value was %q", status.Indicator.Value)
			}
		}()

		go func() {
			defer wg.Done()

			status, err := NewResourceStatus(running)
			if err != nil {
				pendingErrs <- fmt.Sprintf("running: %s", err)
				return
			}
			if !status.IsReady() {
				pendingErrs <- "running resource reported not ready"
			}
			if status.Indicator.Value != "Running" {
				pendingErrs <- fmt.Sprintf("running resource indicator value was %q", status.Indicator.Value)
			}
		}()
	}

	wg.Wait()
	close(pendingErrs)

	var failures []string
	for failure := range pendingErrs {
		failures = append(failures, failure)
	}

	assert.Empty(t, failures, "rules must not be mutated during evaluation: %s", strings.Join(failures, "; "))
}

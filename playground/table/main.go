package main

import (
	"context"

	"github.com/gookit/color"
	"github.com/werf/logboek"

	"github.com/werf/kubedog-for-werf-helm/pkg/utils"
)

func main() {
	_ = logboek.Context(context.Background()).LogProcess("xxx").DoError(func() error {
		_ = logboek.Context(context.Background()).LogProcess("1").DoError(func() error {
			t := utils.NewTable(.7, .1, .1, .1)
			t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
			t.Header("NAME", "REPLICAS", "UP-TO-DATE", "AVAILABLE")
			t.Row("deploy/extended-monitoring", "1/1", 1, 1)
			// t.Row("deploy/extended-monitoring", "1/1", 1, 1, color.RedString("Error: See the server log for details. BUILD FAILED (total time: 1 second)"), color.RedString("Error: An individual language user's deviations from standard language norms in grammar, pronunciation and orthography are sometimes referred to as errors"))
			st := t.SubTable(.3, .15, .3, .15, .1)
			st.Header("NAME", "READY", "STATUS", "RESTARTS", "AGE")
			st.Rows([][]interface{}{
				{"654fc55df-5zs4m", "3/3", "Pulling", "0", "49m", color.Red.Sprint("pod/myapp-backend-cbdb856d7-bvplx Failed: Error: ImagePullBackOff"), color.Red.Sprint("pod/myapp-backend-cbdb856d7-b6ms8 Failed: Failed to pull image \"ubuntu:kaka\": rpc error: code Unknown desc = Error response from daemon: manifest for ubuntu:kaka not found")},
				{"654fc55df-hsm67", "3/3", color.Green.Sprint("Running") + " -> " + color.Red.Sprint("Terminating"), "0", "49m"},
				{"654fc55df-fffff", "3/3", "Ready", "0", "49m"},
				{"654fc55df-5zs4m", "3/3", "Pulling", "0", "49m", color.Red.Sprint("pod/myapp-backend-cbdb856d7-bvplx Failed: Error: ImagePullBackOff"), color.Red.Sprint("pod/myapp-backend-cbdb856d7-b6ms8 Failed: Failed to pull image \"ubuntu:kaka\": rpc error: code Unknown desc = Error response from daemon: manifest for ubuntu:kaka not found")},
			}...)
			st.Commit(color.Red.Sprint("pod/myapp-backend-cbdb856d7-b6ms8 Failed: Failed to pull image \"ubuntu:kaka\": rpc error: code Unknown desc = Error response from daemon: manifest for ubuntu:kaka not found"), color.Red.Sprint("pod/myapp-backend-cbdb856d7-b6ms8 Failed: Failed to pull image \"ubuntu:kaka\": rpc error: code Unknown desc = Error response from daemon: manifest for ubuntu:kaka not found"))
			t.Row("deploy/grafana", "1/1", 1, 1)
			t.Row("deploy/kube-state-metrics", "1/1", 1, 1)
			t.Row("deploy/madison-proxy-0450d21f50d1e3f3b3131a07bcbcfe85ec02dd9758b7ee12968ee6eaee7057fc", "1/1", 1, 1)
			t.Row("deploy/madison-proxy-2c5bdd9ba9f80394e478714dc299d007182bc49fed6c319d67b6645e4812b198", "1/1", 1, 1)
			t.Row("deploy/madison-proxy-9c6b5f859895442cb645c7f3d1ef647e1ed5388c159a9e5f7e1cf50163a878c1", "1/1", 1, "1 (-1)")
			t.Row("deploy/prometheus-metrics-adapter", "1/1", 1, "1 (-1)")
			t.Row("sts/mysql", "1/1", 1, "1 (-1)")
			t.Row("ds/node-exporter", "1/1", 1, "1 (-1)")
			t.Row("deploy/trickster", "1/1", 1, "1 (-1)")
			logboek.Context(context.Background()).Log(t.Render())

			return nil
		})
		return nil
	})
}
